package lanes

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Adopted is what aw adopt did in one of a lane's repositories.
type Adopted struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"` // the lane's own branch, now at the remote branch's tip
	Remote string `json:"remote"` // the remote branch it continues
	Head   string `json:"head"`
}

// Adopt continues an existing branch on origin in a lane (an open merge request, a spike, a
// teammate's branch): it fetches the branch, points the lane's own branch at it, sets the upstream,
// and records it as the name aw push pushes to, so recap, finish and sweep know the lane's merge
// request from the start. It refuses, changing nothing, when a repository has commits or changes of
// the lane's own that moving the branch would lose. Without names it adopts the branch in every
// repository of the lane that has it on origin.
func (m *Manager) Adopt(ctx context.Context, l state.Lane, remote string, only []string) ([]Adopted, error) {
	if _, err := run.In(m.WS.Owner).Run(ctx, "check-ref-format", "--branch", remote); err != nil || strings.HasPrefix(remote, "-") {
		return nil, fmt.Errorf("%q is not a valid branch name → name the branch as it is on origin", remote)
	}
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	var known []string
	for _, r := range repos {
		known = append(known, repoKey(r))
	}
	for _, name := range only {
		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("lane %s has no repository %q → name one of %s", Dir(l.Number, m.Cfg.Lanes.Max), name, strings.Join(known, ", "))
		}
	}
	type plan struct {
		r    state.LaneRepo
		head string
	}
	var missing []string
	var todo []plan
	var problems []string
	for _, r := range repos {
		key := repoKey(r)
		if r.Branch == "" || (len(only) > 0 && !slices.Contains(only, key)) {
			continue
		}
		if _, err := os.Stat(r.Path); err != nil {
			if len(only) > 0 {
				problems = append(problems, fmt.Sprintf("%s has no worktree in this lane yet (aw use %s)", key, key))
			}
			continue
		}
		g := run.In(r.Path)
		if m.Progress != nil {
			m.Progress(fmt.Sprintf("%s: fetching %s from origin", key, remote))
		}
		if _, err := g.Run(ctx, "fetch", "--quiet", "origin", "refs/heads/"+remote+":refs/remotes/origin/"+remote); err != nil {
			missing = append(missing, key)
			continue
		}
		head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+remote+"^{commit}")
		if err != nil {
			missing = append(missing, key)
			continue
		}
		status, err := g.Run(ctx, "status", "--porcelain=v1", "-uall")
		if err != nil {
			return nil, err
		}
		if workspace.WithoutMaterialized(r.Path, status) != "" {
			problems = append(problems, key+" has uncommitted changes")
		}
		if n := ownCommits(ctx, g, r); n > 0 {
			if ok, _ := g.OK(ctx, "merge-base", "--is-ancestor", "HEAD", head); !ok {
				problems = append(problems, fmt.Sprintf("%s has %d commit(s) of the lane's own that %s does not hold", key, n, remote))
			}
		}
		if branch, _ := g.Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD"); branch != r.Branch {
			problems = append(problems, fmt.Sprintf("%s is not on the lane's branch, %s", key, r.Branch))
		}
		todo = append(todo, plan{r, head})
	}
	if len(only) > 0 && len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("origin has no branch %s in %s", remote, strings.Join(missing, ", ")))
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("lane %s cannot adopt %s, and nothing was changed: %s → commit or move that work elsewhere first, or adopt the branch in a new lane", l.Name, remote, strings.Join(problems, "; "))
	}
	if len(todo) == 0 {
		return nil, fmt.Errorf("origin has no branch %s in any repository of lane %s → check the name with git ls-remote origin", remote, l.Name)
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	var out []Adopted
	for _, p := range todo {
		key := repoKey(p.r)
		g := run.In(p.r.Path)
		if _, err := g.Run(ctx, "reset", "--quiet", "--hard", p.head); err != nil {
			return out, fmt.Errorf("%s: moving %s to origin/%s: %w", key, p.r.Branch, remote, err)
		}
		if _, err := g.Run(ctx, "branch", "--quiet", "--set-upstream-to=origin/"+remote, p.r.Branch); err != nil {
			return out, fmt.Errorf("%s: setting the upstream of %s: %w", key, p.r.Branch, err)
		}
		published := remote
		if remote == p.r.Branch {
			published = ""
		}
		if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetPublished(tx, l.Number, p.r.Project, published) }); err != nil {
			return out, fmt.Errorf("%s now continues %s, but that could not be recorded (%v) → run aw adopt again", key, remote, err)
		}
		out = append(out, Adopted{Repo: key, Branch: p.r.Branch, Remote: remote, Head: p.head})
	}
	return out, nil
}
