package lanes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// rehearsalOffset moves a rehearsal's ports clear of the owner's and the lanes'.
const rehearsalOffset = 20000

// Onboarding purposes, which mark the lane aw onboard creates.
const (
	PurposeOnboard = "onboard this workspace"
	PurposeReview  = "review onboarding"
)

// IsOnboarding reports whether a lane is the one aw onboard created.
func IsOnboarding(l state.Lane) bool {
	return l.Purpose == PurposeOnboard || l.Purpose == PurposeReview
}

// Rehearsal is a throwaway copy of the workspace, holding one lane's proposal.
type Rehearsal struct {
	Owner      string
	Namespace  string
	Cfg        *config.Config
	Started    bool // something may be running that belongs to the copy
	individual string
}

// Remove deletes the copy and its individual file.
func (r *Rehearsal) Remove() error {
	if r.individual != "" {
		os.Remove(r.individual)
	}
	return os.RemoveAll(r.Owner)
}

var namespaceLine = regexp.MustCompile(`(?m)^(\s*namespace\s*=\s*)"[^"]*"`)

// BuildRehearsal makes a throwaway owner checkout from a lane: the lane's heads and uncommitted
// changes, the owner's declared secrets and local files, and a namespace and port offset of its own.
// Nothing in it points back at the owner, and a push from it goes nowhere that matters: its origin
// is the owner's checkout. A non-nil result must be removed, even with an error.
func (m *Manager) BuildRehearsal(ctx context.Context, l state.Lane, id string, say func(string)) (*Rehearsal, error) {
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	r := &Rehearsal{
		Owner:     filepath.Join(m.WS.Owner, ".worktrees", workspace.RehearsalPrefix+id),
		Namespace: "rh" + id,
	}
	inGit := slices.ContainsFunc(repos, func(x state.LaneRepo) bool { return m.relOf(x) == "." })
	say("copying the lane")
	if inGit {
		for _, x := range repos {
			if m.relOf(x) == "." {
				if err := m.cloneInto(ctx, x, r.Owner); err != nil {
					return r, err
				}
			}
		}
	} else if err := m.copyPlainRoot(l, r.Owner); err != nil {
		return r, err
	}
	for _, x := range repos {
		if rel := m.relOf(x); rel != "." {
			say("copying " + rel)
			if err := m.cloneInto(ctx, x, filepath.Join(r.Owner, rel)); err != nil {
				return r, err
			}
		}
	}
	marker := filepath.Join(r.Owner, filepath.FromSlash(workspace.RehearsalMarker))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return r, err
	}
	if err := os.WriteFile(marker, []byte("A throwaway copy made by aw rehearse.\n"), 0o644); err != nil {
		return r, err
	}
	team := filepath.Join(r.Owner, config.TeamFile)
	data, err := os.ReadFile(team)
	if err != nil {
		return r, fmt.Errorf("lane %s has no %s to rehearse → write one in the lane first", l.Name, config.TeamFile)
	}
	if !namespaceLine.Match(data) {
		return r, fmt.Errorf("the lane's %s has no namespace → add one under [workspace]", config.TeamFile)
	}
	if err := os.WriteFile(team, namespaceLine.ReplaceAll(data, []byte(`${1}"`+r.Namespace+`"`)), 0o644); err != nil {
		return r, err
	}
	if err := r.writeIndividual(m.Cfg.Workspace.Namespace); err != nil {
		return r, err
	}
	if r.Cfg, err = config.Load(r.Owner); err != nil {
		return r, fmt.Errorf("the lane's configuration is invalid: %w", err)
	}
	if problems := checkIsolated(r.Owner, r.Cfg); len(problems) > 0 {
		return r, fmt.Errorf("this proposal is not isolated, so a rehearsal could reach your real containers and data, and nothing was started:\n  %s\n→ remove a devcontainer's port when a seat publishes it, and otherwise read host ports as ${AW_PORT_<n>:-<n>}, the shared network's name as ${AW_INFRA_NETWORK:-<name>}, and in scripts the infrastructure's Compose project as ${AW_INFRA_PROJECT:-<name>} (aw sets these in a rehearsal)", strings.Join(problems, "\n  "))
	}
	for _, name := range sortedKeys(r.Cfg.Projects) {
		p := r.Cfg.Projects[name]
		for _, f := range slices.Concat(p.Secrets, p.LocalFiles) {
			src := filepath.Join(m.WS.Owner, p.Directory, f)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			dst := filepath.Join(r.Owner, p.Directory, f)
			os.Remove(dst)
			if _, err := copyOne(src, dst); err != nil {
				return r, fmt.Errorf("copying %s: %w", f, err)
			}
		}
	}
	return r, nil
}

// writeIndividual gives the copy the person's own settings, with the ports moved aside.
func (r *Rehearsal) writeIndividual(namespace string) error {
	r.individual = config.IndividualFile(r.Namespace)
	text := ""
	if data, err := os.ReadFile(config.IndividualFile(namespace)); err == nil {
		text = string(data)
	}
	offset := fmt.Sprintf("offset = %d", rehearsalOffset)
	switch {
	case regexp.MustCompile(`(?m)^\s*offset\s*=.*$`).MatchString(text):
		text = regexp.MustCompile(`(?m)^\s*offset\s*=.*$`).ReplaceAllString(text, offset)
	case regexp.MustCompile(`(?m)^\[ports\]\s*$`).MatchString(text):
		text = regexp.MustCompile(`(?m)^\[ports\]\s*$`).ReplaceAllString(text, "[ports]\n"+offset)
	default:
		text += "\n[ports]\n" + offset + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(r.individual), 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.individual, []byte(text), 0o600)
}

// cloneInto clones a repository at the lane's head, on a branch named like the owner's, then lays the
// lane's uncommitted changes over it.
func (m *Manager) cloneInto(ctx context.Context, x state.LaneRepo, dst string) error {
	owner := filepath.Join(m.WS.Owner, m.relOf(x))
	head, err := run.In(x.Path).Line(ctx, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	branch, err := run.In(owner).Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch = "main" // the owner is trying something: any name will do here
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := run.In(filepath.Dir(dst)).Run(ctx, "clone", "--quiet", "--shared", "--no-checkout", "--", owner, dst); err != nil {
		return err
	}
	g := run.In(dst)
	if _, err := g.Run(ctx, "update-ref", "refs/heads/aw-rehearsal-base", head); err != nil {
		return err
	}
	if _, err := g.Run(ctx, "symbolic-ref", "HEAD", "refs/heads/aw-rehearsal-base"); err != nil {
		return err
	}
	if _, err := g.Run(ctx, "reset", "--quiet", "--hard", head); err != nil {
		return err
	}
	if _, err := g.Run(ctx, "branch", "--quiet", "-M", branch); err != nil {
		return err
	}
	if err := overlayChanges(ctx, x.Path, dst); err != nil {
		return err
	}
	// Committed here, in the copy only, because the rehearsal's lanes start from commits.
	if _, err := g.Run(ctx, "add", "-A"); err != nil {
		return err
	}
	if ok(g.OK(ctx, "diff", "--cached", "--quiet")) {
		return nil
	}
	_, err = g.Run(ctx, "-c", "user.name=aw", "-c", "user.email=aw@localhost", "commit", "-q", "--no-verify", "-m", "the lane's uncommitted changes")
	return err
}

// overlayChanges copies what is uncommitted in a lane's worktree into the copy.
func overlayChanges(ctx context.Context, from, to string) error {
	out, err := run.In(from).Run(ctx, "status", "--porcelain=v1", "-uall", "-z", "--", ".", ":(exclude).worktrees")
	if err != nil {
		return err
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		code, path := e[:2], e[3:]
		if strings.ContainsAny(code, "RC") {
			i++
		}
		src, dst := filepath.Join(from, filepath.FromSlash(path)), filepath.Join(to, filepath.FromSlash(path))
		os.Remove(dst)
		if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
			continue // deleted in the lane
		}
		if _, err := copyOne(src, dst); err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}
	}
	return nil
}

// copyPlainRoot copies a lane root that is not a worktree: its files, and not its links to projects.
func (m *Manager) copyPlainRoot(l state.Lane, dst string) error {
	root := m.Root(l.Number)
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." || d.Type()&os.ModeSymlink != 0 || neverCopied(filepath.ToSlash(rel)) || d.IsDir() {
			return err
		}
		_, err = copyOne(p, filepath.Join(dst, rel))
		return err
	})
}

var (
	literalPort   = regexp.MustCompile(`^\s*-\s*["']?(?:[0-9.]+:)?[0-9]+:[0-9]+`)
	publishedPort = regexp.MustCompile(`^\s*published:\s*["']?[0-9]`)
	containerName = regexp.MustCompile(`^\s*container_name:\s*[^$\s]`)
	externalTrue  = regexp.MustCompile(`^\s*external:\s*true`)
	nameLine      = regexp.MustCompile(`^\s*name:\s*["']?([^"'\s#]+)`)
	topName       = regexp.MustCompile(`(?m)^name:\s*["']?([^"'$\s#]+)`)
)

// isolationSkipped are directories that hold no commands a rehearsal would run.
var isolationSkipped = []string{".git", ".worktrees", ".aw", ".claude", ".agents", ".codex", ".grok", ".opencode", "docs", "plans", "node_modules"}

// checkIsolated finds what would connect a rehearsal to the real workspace: aw can move what it names,
// but not a port, network or Compose project written into a file. Guards fail closed, so any of
// these stops the rehearsal before it starts anything.
func checkIsolated(owner string, c *config.Config) []string {
	var problems []string
	var files []string
	infra, _ := filepath.Glob(filepath.Join(owner, "infra", "*.y*ml"))
	more, _ := filepath.Glob(filepath.Join(owner, "infra", "compose.d", "*.y*ml"))
	files = append(infra, more...)
	roots := []string{owner}
	for _, name := range sortedKeys(c.Projects) {
		p := c.Projects[name]
		if p.Runner == "devcontainer" {
			f, _ := filepath.Glob(filepath.Join(owner, p.Directory, ".devcontainer", "*.y*ml"))
			files = append(files, f...)
			roots = append(roots, filepath.Join(owner, p.Directory, ".devcontainer"))
		}
	}
	names := map[string]bool{}
	rel := func(f string) string { r, _ := filepath.Rel(owner, f); return filepath.ToSlash(r) }
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m := topName.FindSubmatch(data); m != nil {
			names[string(m[1])] = true
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			at := fmt.Sprintf("%s:%d", rel(f), i+1)
			switch {
			case strings.Contains(line, "$"):
			case literalPort.MatchString(line), publishedPort.MatchString(line):
				what := "a fixed host port: "
				if strings.Contains(filepath.ToSlash(f), "/.devcontainer/") {
					what = "a fixed host port, which aw already publishes if it is a seat's, so remove it then: "
				}
				problems = append(problems, at+": "+what+strings.TrimSpace(line))
			case containerName.MatchString(line):
				problems = append(problems, at+": a fixed container name: "+strings.TrimSpace(line))
			case externalTrue.MatchString(line) && !nearbyVariableName(lines, i):
				problems = append(problems, at+": a fixed shared network")
			}
		}
	}
	for name, d := range c.Lanes.Data {
		if (d.Provider == "postgres" || d.Provider == "redis") && d.Service == "" {
			problems = append(problems, "[lanes.data."+name+"]: connects by URL, which reaches whatever listens there → set service")
		}
	}
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && (slices.Contains(isolationSkipped, d.Name()) || (root == owner && isProjectDir(owner, p, c))) {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if info, err := d.Info(); err != nil || info.Size() > 1<<20 || !info.Mode().IsRegular() || ext == ".md" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".crt" {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "AW_") {
					continue
				}
				for name := range names {
					if strings.Contains(line, name) {
						problems = append(problems, fmt.Sprintf("%s:%d: names %s, a real Compose project", rel(p), i+1, name))
					}
				}
			}
			return nil
		})
	}
	slices.Sort(problems)
	return slices.Compact(problems)
}

// nearbyVariableName reports whether an external network's name, just above it, is a variable.
func nearbyVariableName(lines []string, i int) bool {
	for j := i - 1; j >= 0 && j >= i-3; j-- {
		if m := nameLine.FindStringSubmatch(lines[j]); m != nil {
			return strings.Contains(lines[j], "$")
		}
	}
	return false
}

func isProjectDir(owner, p string, c *config.Config) bool {
	for _, pr := range c.Projects {
		if pr.Directory != "." && filepath.Join(owner, pr.Directory) == p {
			return true
		}
	}
	return false
}
