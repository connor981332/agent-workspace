package lanes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Readiness is whether a lane could be created in one repository, found without creating anything.
type Readiness struct {
	Project string // "" for the workspace repository
	Blocked string // why a lane cannot be created, with its fix
	Warning string
	Note    string
}

// CheckReadiness dry-runs lane creation for each repository (aw doctor). It only reads. The Manager
// needs no state.
func (m *Manager) CheckReadiness(ctx context.Context) []Readiness {
	var out []Readiness
	if !m.plainRoot(ctx) && !slices.ContainsFunc(sortedKeys(m.Cfg.Projects), func(n string) bool { return m.Cfg.Projects[n].Directory == "." }) {
		r := Readiness{}
		if _, err := m.planRepo(ctx, "", ".", "", nil); err != nil {
			r.Blocked = strings.TrimPrefix(err.Error(), "no lane was created: ")
		}
		out = append(out, r)
	}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		p := m.Cfg.Projects[name]
		r := Readiness{Project: name}
		dir := filepath.Join(m.WS.Owner, p.Directory)
		if p.Lanes == "shared" {
			continue
		}
		if !workspace.IsGitRepo(dir) {
			r.Warning = fmt.Sprintf("%s is not cloned, so no lane can be created → run aw setup, or clone it into %s", name, p.Directory)
			out = append(out, r)
			continue
		}
		plan, err := m.planRepo(ctx, name, p.Directory, "", p.Secrets)
		if err == nil {
			err = checkGuards(ctx, name, plan, p.LaneGuards)
		}
		if err != nil {
			r.Blocked = strings.TrimPrefix(err.Error(), "no lane was created: ")
			out = append(out, r)
			continue
		}
		var missing []string
		for _, f := range slices.Concat(p.Secrets, p.LocalFiles) {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			r.Warning = fmt.Sprintf("the owner checkout has no %s, so lanes will not have it → get it from your team, and put it in %s", strings.Join(missing, ", "), p.Directory)
		}
		if p.PostCheckout != "skip" {
			if h := run.HookPath(ctx, dir, "post-checkout"); h != "" {
				r.Note = "its post-checkout hook runs as a setup step of each lane; a failure there leaves the lane incomplete, and aw setup <lane> retries it"
			}
		}
		out = append(out, r)
	}
	return out
}

// Orphan is a branch or a worktree that looks like a lane's and that no lane records: what a failed
// or interrupted lane left.
type Orphan struct {
	Repo     string `json:"repo"`  // the owner's checkout of it
	Label    string `json:"label"` // for messages
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

func canonical(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// FindOrphans lists the leftovers, given every repository the state records. It only reads.
func (m *Manager) FindOrphans(ctx context.Context, recorded []state.LaneRepo) []Orphan {
	branches, paths := map[string]bool{}, map[string]bool{}
	for _, r := range recorded {
		branches[repoKey(r)+"\x00"+r.Branch] = true
		paths[canonical(r.Path)] = true
	}
	type repo struct{ key, label, dir string }
	var repos []repo
	if workspace.IsGitRepo(m.WS.Owner) {
		repos = append(repos, repo{".", "the workspace repository", m.WS.Owner})
	}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		p := m.Cfg.Projects[name]
		if dir := filepath.Join(m.WS.Owner, p.Directory); p.Directory != "." && p.Lanes != "shared" && workspace.IsGitRepo(dir) {
			repos = append(repos, repo{name, "project " + name, dir})
		}
	}
	var out []Orphan
	for _, r := range repos {
		g := run.In(r.dir)
		listed, err := g.Run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
		if err != nil {
			continue
		}
		seen := map[string]int{}
		for _, b := range strings.Fields(listed) {
			single := r.key == "." && slices.ContainsFunc(recorded, func(x state.LaneRepo) bool { return x.Branch == b })
			if strings.HasPrefix(b, m.Cfg.Lanes.BranchPrefix) && !branches[r.key+"\x00"+b] && !single {
				seen[b] = len(out)
				out = append(out, Orphan{Repo: r.dir, Label: r.label, Branch: b})
			}
		}
		trees, err := g.Run(ctx, "worktree", "list", "--porcelain")
		if err != nil {
			continue
		}
		prefix := canonical(filepath.Join(r.dir, ".worktrees")) + string(filepath.Separator)
		path := ""
		for _, line := range strings.Split(trees, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				path = p
				if !strings.HasPrefix(canonical(p), prefix) || paths[canonical(p)] {
					path = ""
				}
			}
			if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && path != "" {
				if i, ok := seen[b]; ok {
					out[i].Worktree = path
				} else {
					out = append(out, Orphan{Repo: r.dir, Label: r.label, Worktree: path})
				}
				path = ""
			}
		}
	}
	return out
}

// DiscardedOrphan is one leftover aw discard --orphans dealt with.
type DiscardedOrphan struct {
	Orphan
	Archive string `json:"archive,omitempty"`
	Skipped string `json:"skipped,omitempty"` // why it was left
}

var notRefWord = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// DiscardOrphans archives each leftover branch, then removes it and its worktree. A worktree with
// uncommitted files is left for the human, and said so.
func (m *Manager) DiscardOrphans(ctx context.Context, now time.Time) ([]DiscardedOrphan, error) {
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	all, err := state.Lanes(m.Store.DB())
	if err != nil {
		return nil, err
	}
	var recorded []state.LaneRepo
	for _, l := range all {
		if l.State == state.LaneClaiming {
			return nil, fmt.Errorf("lane %s is still being created, so its branches would look like leftovers → try again when it is done", Dir(l.Number, m.Cfg.Lanes.Max))
		}
		repos, err := state.LaneRepos(m.Store.DB(), l.Number)
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, repos...)
	}
	stamp := now.UTC().Format("20060102-150405")
	var out []DiscardedOrphan
	for _, o := range m.FindOrphans(ctx, recorded) {
		d := DiscardedOrphan{Orphan: o}
		g := run.In(o.Repo, noHooks...)
		if o.Worktree != "" {
			status, err := run.In(o.Worktree).Run(ctx, "status", "--porcelain=v1", "-uall")
			if err != nil || status != "" {
				d.Skipped = fmt.Sprintf("its worktree %s has uncommitted files → commit or remove them there, then run this again", o.Worktree)
				out = append(out, d)
				continue
			}
		}
		if o.Branch != "" {
			head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+o.Branch)
			if err != nil {
				return out, err
			}
			d.Archive = fmt.Sprintf("refs/aw/archive/orphan-%s/%s", notRefWord.ReplaceAllString(o.Branch, "-"), stamp)
			if _, err := g.Run(ctx, "update-ref", d.Archive, head, ""); err != nil {
				return out, fmt.Errorf("archiving %s in %s: %w; it was left in place", o.Branch, o.Label, err)
			}
		}
		if o.Worktree != "" {
			if _, err := g.Run(ctx, "worktree", "remove", o.Worktree); err != nil {
				return out, err
			}
		}
		if o.Branch != "" {
			if _, err := g.Run(ctx, "branch", "-D", o.Branch); err != nil {
				return out, err
			}
		}
		out = append(out, d)
	}
	return out, nil
}
