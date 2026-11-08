// Package cmds holds the acceptance suite's custom testscript commands (plans/release-candidate.md
// §3.3). Each is documented in acceptance/README.md, so that another runner could interpret the
// same scenario files.
package cmds

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

// Config is what the runner knows before any scenario starts.
type Config struct {
	AWBin    string // the aw binary under test
	GlobalAW string // directory holding the suite's global aw (a proxy to a workspace's bin/aw)
	StubsDir string // acceptance/stubs
	RealGit  string // absolute path of the real git
	Repo     string // the repository under test, for scenarios that check its examples
	Version  string // the version of the binary under test
	Mirror   string // a release mirror holding it, for AW_MIRROR
}

// FinalCheck is the command the runner appends to every scenario: the owner checkout must be
// unchanged, unless the scenario said owner-may-change.
const FinalCheck = "owner-check-final"

type state struct {
	cfg       Config
	owner     string   // the owner checkout, as the scenario sees it (not canonicalized)
	repos     []string // repositories in the owner checkout, relative to it ("." is the owner itself)
	snap      snapshot
	mayChange bool
	pathDirs  map[string]bool // directories already put on PATH
	gitFail   string          // file of git-fail patterns
	// ownerBranches are the branches each repository had when the workspace was built, keyed by
	// "<repo>\x00<branch>".
	ownerBranches map[string]bool
}

func get(ts *testscript.TestScript) *state { return ts.Value(stateKey{}).(*state) }

type stateKey struct{}

// Setup gives every scenario an isolated home, Git configuration and stub state.
func Setup(cfg Config) func(*testscript.Env) error {
	return func(env *testscript.Env) error {
		work := env.WorkDir
		home := filepath.Join(work, ".home")
		for _, d := range []string{home, filepath.Join(work, ".stub", "state")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		gitconfig := filepath.Join(home, ".gitconfig")
		if err := os.WriteFile(gitconfig, []byte(gitConfig), 0o644); err != nil {
			return err
		}
		path := cfg.GlobalAW + string(os.PathListSeparator) + os.Getenv("PATH")
		env.Vars = append(env.Vars,
			"HOME="+home,
			"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
			"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
			"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
			"GIT_CONFIG_GLOBAL="+gitconfig,
			"GIT_CONFIG_NOSYSTEM=1",
			"STUB_LOG="+filepath.Join(work, ".stub", "log"),
			"STUB_STATE="+filepath.Join(work, ".stub", "state"),
			"AW_BIN="+cfg.AWBin,
			"AW_REPO="+cfg.Repo,
			"AW_VERSION="+cfg.Version,
			"AW_MIRROR="+cfg.Mirror,
			"LC_ALL=C",
			"PATH="+path,
		)
		env.Values[stateKey{}] = &state{cfg: cfg, pathDirs: map[string]bool{}}
		return nil
	}
}

const gitConfig = `[user]
	name = Acceptance Owner
	email = owner@example.invalid
[init]
	defaultBranch = main
[advice]
	detachedHead = false
`

// Condition adds the suite's conditions: [root] (running as root, which can write anywhere).
func Condition(cond string) (bool, error) {
	switch cond {
	case "root":
		return os.Geteuid() == 0, nil
	}
	return false, fmt.Errorf("unknown condition %q", cond)
}

// Commands returns the custom commands.
func Commands() map[string]func(ts *testscript.TestScript, neg bool, args []string) {
	return map[string]func(ts *testscript.TestScript, neg bool, args []string){
		"workspace":        cmdWorkspace,
		"stubs":            cmdStubs,
		"json":             cmdJSON,
		"owner-snapshot":   cmdOwnerSnapshot,
		"owner-unchanged":  cmdOwnerUnchanged,
		"owner-may-change": cmdOwnerMayChange,
		"git-fail":         cmdGitFail,
		"unwritten":        cmdUnwritten,
		FinalCheck:         cmdOwnerCheckFinal,
	}
}

// git runs the real Git as the human, in dir, with the scenario's Git configuration. It is for
// building fixtures and reading them, so it bypasses git-fail and the stubs. A failure fails the
// scenario.
func git(ts *testscript.TestScript, dir string, args ...string) string {
	out, err := gitRun(ts, dir, args...)
	if err != nil {
		ts.Fatalf("%v", err)
	}
	return out
}

func gitRun(ts *testscript.TestScript, dir string, args ...string) (string, error) {
	cmd := exec.Command(get(ts).cfg.RealGit, args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"HOME=" + ts.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + ts.Getenv("TMPDIR"),
		"GIT_CONFIG_GLOBAL=" + ts.Getenv("GIT_CONFIG_GLOBAL"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg += ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s (in %s): %s", strings.Join(args, " "), dir, msg)
	}
	return string(out), nil
}

// prependPath puts dir first on the scenario's PATH, once.
func prependPath(ts *testscript.TestScript, dir string) {
	st := get(ts)
	if st.pathDirs[dir] {
		return
	}
	st.pathDirs[dir] = true
	ts.Setenv("PATH", dir+string(os.PathListSeparator)+ts.Getenv("PATH"))
}

// workspace <name> [--single] [--repos a,b] [--forge github|gitlab|none]
func cmdWorkspace(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! workspace")
	}
	st := get(ts)
	if st.owner != "" {
		ts.Fatalf("workspace: this scenario already has a workspace at %s", st.owner)
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		ts.Fatalf("usage: workspace <name> [--single] [--init] [--repos a,b] [--forge github|gitlab|none]")
	}
	name, single, repos, forge, initialize := args[0], false, []string{"web", "api"}, "github", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--single":
			single = true
		case "--init":
			initialize = true
		case "--repos", "--forge":
			if i+1 == len(args) {
				ts.Fatalf("workspace: %s needs a value", args[i])
			}
			if args[i] == "--repos" {
				repos = strings.Split(args[i+1], ",")
			} else {
				forge = args[i+1]
			}
			i++
		default:
			ts.Fatalf("workspace: unknown argument %q", args[i])
		}
	}
	switch forge {
	case "github", "gitlab", "none":
	default:
		ts.Fatalf("workspace: --forge must be github, gitlab or none")
	}

	work := ts.Getenv("WORK")
	// The owner checkout's path contains spaces and a quote (C1 practice), and is reached through
	// a symlink, so that every path aw prints or compares has to survive canonicalization. On
	// macOS the symlink is the system's own /var → /private/var.
	real := filepath.Join(work, "real")
	link := filepath.Join(work, "link")
	ts.Check(os.MkdirAll(real, 0o755))
	ts.Check(os.Symlink(real, link))
	base := link
	if runtime.GOOS == "darwin" {
		if p, ok := strings.CutPrefix(base, "/private/var/"); ok {
			base = "/var/" + p
		}
	}
	owner := filepath.Join(base, "it's a workspace", name)
	ts.Check(os.MkdirAll(owner, 0o755))

	remotes := filepath.Join(work, "remotes")
	ts.Check(os.MkdirAll(remotes, 0o755))
	newRepo := func(dir, remoteName string, files map[string]string) {
		git(ts, dir, "init", "-q", "-b", "main")
		for f, content := range files {
			ts.Check(os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644))
		}
		git(ts, dir, "add", "-A")
		git(ts, dir, "commit", "-q", "-m", "Initial commit")
		bare := filepath.Join(remotes, remoteName+".git")
		git(ts, remotes, "init", "-q", "--bare", "-b", "main", bare)
		git(ts, dir, "remote", "add", "origin", bare)
		git(ts, dir, "push", "-q", "-u", "origin", "main")
	}

	// workspace.toml comes from the scenario's archive, when it has one. With --init, aw init writes
	// it first and the archive's then replaces it.
	files := map[string]string{"README.md": "# " + name + "\n"}
	team, haveTeam := os.ReadFile(filepath.Join(work, "workspace.toml"))
	if haveTeam == nil {
		// Consumed: left at $WORK, it would make $WORK itself look like a workspace.
		ts.Check(os.Rename(filepath.Join(work, "workspace.toml"), filepath.Join(work, ".workspace.toml")))
	}
	if haveTeam == nil && !initialize {
		files["workspace.toml"] = string(team)
	}
	if single {
		repos = []string{"."}
	} else {
		files[".gitignore"] = "/" + strings.Join(repos, "/\n/") + "/\n"
	}
	newRepo(owner, name, files)
	if !single {
		for _, r := range repos {
			if r == "" || strings.ContainsAny(r, `/\`) || r == "." || r == ".." {
				ts.Fatalf("workspace: bad repository name %q", r)
			}
			dir := filepath.Join(owner, r)
			ts.Check(os.MkdirAll(dir, 0o755))
			newRepo(dir, r, map[string]string{"README.md": "# " + r + "\n"})
		}
		repos = append([]string{"."}, repos...)
	}

	st.owner, st.repos = owner, repos
	ts.Setenv("OWNER", owner)
	ts.Setenv("AW_TEST_FORGE", forge)
	ts.Check(ts.Chdir(owner))

	if initialize {
		// As a maintainer would: aw init, commit and push what it wrote, and warm the binary cache.
		ts.Check(ts.Exec("aw", "init"))
		if haveTeam == nil {
			ts.Check(os.WriteFile(filepath.Join(owner, "workspace.toml"), team, 0o644))
		}
		git(ts, owner, "add", "-A")
		git(ts, owner, "commit", "-q", "-m", "Set up Agent Workspace")
		git(ts, owner, "push", "-q")
		ts.Check(ts.Exec("aw", "version"))
	}
	st.snap = takeSnapshot(ts)
}

// stubs <name>…
func cmdStubs(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: stubs <name>…")
	}
	dir := filepath.Join(ts.Getenv("WORK"), ".stubs")
	ts.Check(os.MkdirAll(dir, 0o755))
	for _, name := range args {
		data, err := os.ReadFile(filepath.Join(get(ts).cfg.StubsDir, name))
		if err != nil {
			ts.Fatalf("stubs: no stub named %q in acceptance/stubs", name)
		}
		ts.Check(os.WriteFile(filepath.Join(dir, name), data, 0o755))
	}
	prependPath(ts, dir)
}

// git-fail <args-regexp>
func cmdGitFail(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: git-fail <args-regexp>")
	}
	st := get(ts)
	dir := filepath.Join(ts.Getenv("WORK"), ".git-fail")
	if st.gitFail == "" {
		ts.Check(os.MkdirAll(dir, 0o755))
		st.gitFail = filepath.Join(dir, "patterns")
		ts.Check(os.WriteFile(st.gitFail, nil, 0o644))
		script := fmt.Sprintf(gitFailScript, shQuote(st.gitFail), shQuote(st.cfg.RealGit))
		ts.Check(os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755))
	}
	f, err := os.OpenFile(st.gitFail, os.O_APPEND|os.O_WRONLY, 0)
	ts.Check(err)
	_, err = fmt.Fprintln(f, args[0])
	ts.Check(err)
	ts.Check(f.Close())
	prependPath(ts, dir)
}

const gitFailScript = `#!/bin/sh
# Written by the acceptance suite's git-fail command: fail for matching arguments, else run Git.
args="$*"
while IFS= read -r re; do
	if printf '%%s\n' "$args" | grep -Eq -- "$re"; then
		echo "git-fail: injected failure for: git $args" >&2
		exit 1
	fi
done < %s
exec %s "$@"
`

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// unwritten: a placeholder in a pending scenario. It fails, so a scenario cannot pass by being
// left unwritten when its milestone is reached.
func cmdUnwritten(ts *testscript.TestScript, neg bool, args []string) {
	ts.Fatalf("this scenario is not written yet: replace 'unwritten' with its script")
}
