package lanes

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
)

// PushResult is what aw push did in one of a lane's repositories.
type PushResult struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Remote  string `json:"remote,omitempty"` // the branch's name on origin
	Outcome string `json:"outcome"`          // pushed, nothing to push, failed
	Detail  string `json:"detail,omitempty"`
}

// Failed reports whether the push failed.
func (r PushResult) Failed() bool { return r.Outcome == "failed" }

// Push pushes a lane's branch to origin, under the team's name for it when that differs from aw's
// (plans/agent-feedback.md, item 9). The lane keeps its own branch, and the upstream is set to the
// remote name, which is recorded so that recap, finish and sweep find the change on the forge.
//
// Without names it pushes every repository with the lane's own commits, and every one pushed before.
// as is the remote name; "" keeps the name a repository was pushed under before, or aw's own.
func (m *Manager) Push(ctx context.Context, l state.Lane, as string, only []string) ([]PushResult, error) {
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	if as != "" {
		if _, err := run.In(m.WS.Owner).Run(ctx, "check-ref-format", "--branch", as); err != nil || strings.HasPrefix(as, "-") {
			return nil, fmt.Errorf("%q is not a valid branch name → choose another with --as", as)
		}
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
	var out []PushResult
	for _, r := range repos {
		key := repoKey(r)
		g := run.In(r.Path)
		if len(only) > 0 && !slices.Contains(only, key) {
			continue
		}
		if len(only) == 0 && r.Published == "" && ownCommits(ctx, g, r) == 0 {
			continue
		}
		name := as
		if name == "" {
			name = r.RemoteBranch()
		}
		res := PushResult{Repo: key, Branch: r.Branch, Remote: name}
		if r.Published != "" && r.Published != name {
			res.Detail = "it was pushed as " + r.Published + " before, which is left on origin"
		}
		if m.Progress != nil {
			m.Progress(fmt.Sprintf("%s: pushing %s to origin as %s", key, r.Branch, name))
		}
		if _, err := g.Run(ctx, "push", "--quiet", "--set-upstream", "origin", "refs/heads/"+r.Branch+":refs/heads/"+name); err != nil {
			res.Outcome, res.Detail = "failed", err.Error()
			out = append(out, res)
			continue
		}
		res.Outcome = "pushed"
		published := name
		if name == r.Branch {
			published = ""
		}
		if published != r.Published {
			if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetPublished(tx, l.Number, r.Project, published) }); err != nil {
				return out, fmt.Errorf("%s was pushed as %s, but that could not be recorded (%v) → run aw push again", key, name, err)
			}
		}
		out = append(out, res)
	}
	if len(out) == 0 {
		return []PushResult{{Repo: "", Outcome: "nothing to push", Detail: "no repository in this lane has commits of its own"}}, nil
	}
	return out, nil
}
