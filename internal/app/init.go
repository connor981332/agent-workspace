// Package app holds the flows that span several components: init, new, attach, finish, sweep,
// serve, setup and pair.
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"text/template"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/launcher"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// InitOptions configures aw init.
type InitOptions struct {
	Dir       string // the workspace repository's working tree
	Name      string // display name; default from the directory
	Namespace string // default from the directory
	Version   string // the version to pin: this binary's
	Mirror    string // where the release's checksums come from
}

// InitResult is what aw init wrote.
type InitResult struct {
	Owner     string   `json:"owner"`
	Version   string   `json:"version"`
	Namespace string   `json:"namespace"`
	Projects  []string `json:"projects"`
	Files     []string `json:"files"`
	Platforms []string `json:"platforms"`
	Missing   []string `json:"missing_platforms"`
}

// ManagedPath lists the hashes of the files aw manages, so that later upgrades can detect hand
// edits (R18).
const ManagedPath = ".aw/managed"

// Init creates a workspace in a directory, which may be a Git repository or plain files.
func Init(opts InitOptions) (*InitResult, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	inGit := workspace.IsGitRepo(dir)
	if inGit {
		if _, err := workspace.GitCommonDir(dir); err != nil {
			return nil, err
		}
	}
	for _, f := range []string{config.TeamFile, launcher.VersionPath, launcher.LockPath, launcher.ScriptPath, ManagedPath} {
		if _, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			return nil, fmt.Errorf("%s already has %s, so it is already a workspace; nothing was changed → use aw doctor to check it", dir, f)
		}
	}
	if parent := filepath.Dir(dir); parent != dir {
		if ws, err := workspace.Find(parent); err == nil {
			return nil, fmt.Errorf("%s is inside the workspace %s; nothing was changed → create the workspace outside it", dir, ws.Owner)
		}
	}
	if !launcher.Exact(opts.Version) {
		return nil, fmt.Errorf("this aw is version %q, a development build that cannot be pinned → run aw init with a released aw (see install.sh), or set AW_TOOL_PATH in the workspace to use a local build", opts.Version)
	}

	projects, err := detectProjects(dir, filepath.Base(dir))
	if err != nil {
		return nil, err
	}

	lock, platforms, err := launcher.Lock(opts.Mirror, opts.Version)
	if err != nil {
		return nil, err
	}
	here := runtime.GOOS + "_" + runtime.GOARCH
	if !slices.Contains(platforms, here) {
		return nil, fmt.Errorf("the checksums for aw %s cover %s, not this machine (%s) → check the release", opts.Version, strings.Join(platforms, ", "), here)
	}
	var missing []string
	for _, p := range launcher.Platforms {
		if !slices.Contains(platforms, p) {
			missing = append(missing, p)
		}
	}

	base := filepath.Base(dir)
	name := opts.Name
	if name == "" {
		name = base
	}
	namespace := opts.Namespace
	if namespace == "" {
		namespace = deriveNamespace(base)
	}
	var team bytes.Buffer
	if err := teamTemplate.Execute(&team, map[string]any{"Name": name, "Namespace": namespace, "Schema": config.SchemaVersion, "Projects": projects}); err != nil {
		return nil, err
	}
	if _, err := config.LoadBytes(filepath.Join(dir, config.TeamFile), team.Bytes()); err != nil {
		return nil, fmt.Errorf("the generated workspace.toml is invalid (%v) → pass --name and --namespace, or rename the project directories", err)
	}

	sum := sha256.Sum256(launcher.Script)
	managed := fmt.Sprintf("# Hashes of the files aw manages. aw upgrade refuses to overwrite one that was edited by hand.\n%s  %s\n",
		hex.EncodeToString(sum[:]), launcher.ScriptPath)
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{config.TeamFile, team.Bytes(), 0o644},
		{launcher.VersionPath, []byte(opts.Version + "\n"), 0o644},
		{launcher.LockPath, lock, 0o644},
		{launcher.ScriptPath, launcher.Script, 0o755},
		{ManagedPath, []byte(managed), 0o644},
	}
	res := &InitResult{Owner: dir, Version: opts.Version, Namespace: namespace, Platforms: platforms, Missing: missing}
	for _, f := range files {
		path := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, f.data, f.mode); err != nil {
			return nil, err
		}
		if err := os.Chmod(path, f.mode); err != nil {
			return nil, err
		}
		res.Files = append(res.Files, f.path)
	}

	ignore := []string{"/.worktrees/", "/.aw/owner"}
	for _, p := range projects {
		res.Projects = append(res.Projects, p.Name)
		if p.Directory != "." {
			// No trailing slash: it ignores the repository in the owner checkout and the link to
			// the project's worktree in each lane root.
			ignore = append(ignore, "/"+p.Directory)
		}
	}
	if inGit {
		added, err := ensureIgnored(filepath.Join(dir, ".gitignore"), ignore)
		if err != nil {
			return nil, err
		}
		if added {
			res.Files = append(res.Files, ".gitignore")
		}
	}

	// Compile the configuration cache, which hooks read instead of the TOML files.
	if c, err := config.Load(dir); err == nil {
		ws := workspace.Workspace{Owner: dir}
		if state, err := ws.StateDir(); err == nil {
			if err := config.WriteCache(filepath.Join(state, "config.cache"), opts.Version, c); err != nil {
				return nil, fmt.Errorf("writing the configuration cache: %w", err)
			}
		}
	}
	return res, nil
}

type initProject struct {
	Name      string
	Directory string
}

var teamTemplate = template.Must(template.New("workspace.toml").Parse(`# The team layer of this workspace's configuration. The reference is docs/reference/config.md in
# the Agent Workspace repository; aw config explain shows where any key's value comes from.

[workspace]
name = {{ printf "%q" .Name }}
namespace = {{ printf "%q" .Namespace }}
schema = {{ .Schema }}

# What agents may do from their lanes without asking: push only the lane's own branch, open a merge
# request, and comment on or merge the lane's own. Force pushes and deleting branches are never allowed.
[publication]
allow = ["push", "open-merge-request", "merge"]
{{ range .Projects }}
[projects.{{ .Name }}]
{{- if ne .Directory .Name }}
directory = {{ printf "%q" .Directory }}
{{- end }}
{{ end -}}
`))

var nonName = regexp.MustCompile(`[^a-z0-9]+`)

func deriveNamespace(base string) string {
	ns := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(base))
	ns = strings.TrimLeft(ns, "0123456789")
	if len(ns) > 20 {
		ns = ns[:20]
	}
	if ns == "" {
		ns = "workspace"
	}
	return ns
}

func projectName(dir string) string {
	n := strings.Trim(nonName.ReplaceAllString(strings.ToLower(dir), "-"), "-")
	if n != "" && (n[0] < 'a' || n[0] > 'z') {
		n = "p-" + n
	}
	if len(n) > 40 {
		n = strings.TrimRight(n[:40], "-")
	}
	return n
}

// detectProjects finds the project repositories: directories directly inside the workspace that
// are Git repositories. With none, the workspace repository is its own single project.
func detectProjects(dir, base string) ([]initProject, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []initProject
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "bin" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, e.Name(), ".git")); err != nil {
			continue
		}
		name := projectName(e.Name())
		if name == "" || name == "all" {
			return nil, fmt.Errorf("the project directory %q gives no usable project name → rename the directory", e.Name())
		}
		if other, dup := seen[name]; dup {
			return nil, fmt.Errorf("the directories %q and %q would both be project %s → rename one of them", other, e.Name(), name)
		}
		seen[name] = e.Name()
		out = append(out, initProject{Name: name, Directory: e.Name()})
	}
	if len(out) == 0 && !workspace.IsGitRepo(dir) {
		return nil, fmt.Errorf("%s has no Git repositories in it, and is not one → clone your projects into it, or run git init there", dir)
	}
	if len(out) == 0 {
		name := projectName(base)
		if name == "" || name == "all" {
			name = "app"
		}
		out = []initProject{{Name: name, Directory: "."}}
	}
	return out, nil
}

// EnsureIgnored appends the entries missing from a .gitignore.
func EnsureIgnored(path string, entries []string) (bool, error) { return ensureIgnored(path, entries) }

// ensureIgnored appends the entries missing from a .gitignore.
func ensureIgnored(path string, entries []string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, e := range entries {
		if !have[e] {
			add = append(add, e)
		}
	}
	if len(add) == 0 {
		return false, nil
	}
	var b bytes.Buffer
	b.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteByte('\n')
	}
	if len(data) > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("# Agent Workspace: lanes, the lane's owner pointer, and the project repositories\n")
	for _, e := range add {
		b.WriteString(e + "\n")
	}
	return true, os.WriteFile(path, b.Bytes(), 0o644)
}
