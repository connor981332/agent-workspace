package lanes

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// ResetResult is what aw reset-branch did in one repository.
type ResetResult struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Commits  int    `json:"commits"`            // the lane's own commits, now archived
	Archived string `json:"archived,omitempty"` // the ref that keeps them
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Base     string `json:"base,omitempty"` // the base it was reset to, such as origin/main
}

// ResetBranch starts a lane's repositories over from their base, keeping what was there: each
// branch's tip is archived under refs/aw/archive/<lane>/<time> first, as aw discard does, then the
// branch is reset to the base branch as last fetched (plans/agent-feedback.md, item 10). Because it
// archives first, nothing committed is lost, and an agent can run it where a harness would hold back
// a git reset --hard. A repository with uncommitted work refuses the whole reset, and one with no
// commits of the lane's own is left alone.
func (m *Manager) ResetBranch(ctx context.Context, l state.Lane, only []string, now time.Time) ([]ResetResult, error) {
	if l.State != state.LaneWorking && l.State != state.LaneReady {
		return nil, fmt.Errorf("lane %s is %s, so it has no branch to reset", l.Name, l.State)
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
			return nil, fmt.Errorf("lane %s has no repository %q → name one of %s", l.Name, name, strings.Join(known, ", "))
		}
	}
	type target struct {
		r    state.LaneRepo
		own  int
		head string
		base string
	}
	var targets []target
	var dirty []string
	for _, r := range repos {
		if r.Branch == "" || (len(only) > 0 && !slices.Contains(only, repoKey(r))) {
			continue
		}
		g := run.In(r.Path)
		own := ownCommits(ctx, g, r)
		if own == 0 {
			continue
		}
		status, err := g.Run(ctx, "status", "--porcelain=v1", "-uall")
		if err != nil {
			return nil, err
		}
		if status = workspace.WithoutMaterialized(r.Path, status); status != "" {
			dirty = append(dirty, fmt.Sprintf("%s (%d)", repoLabel(r), strings.Count(status, "\n")))
			continue
		}
		head, err := g.Line(ctx, "rev-parse", "--verify", "refs/heads/"+r.Branch)
		if err != nil {
			return nil, fmt.Errorf("%s: the lane's branch %s cannot be read: %w", repoLabel(r), r.Branch, err)
		}
		base, err := g.Line(ctx, "rev-parse", "--verify", r.BaseRef+"^{commit}")
		if r.BaseRef == "" || err != nil {
			base = r.BaseSHA
		}
		targets = append(targets, target{r, own, head, base})
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("lane %s has uncommitted work in %s, which a reset would lose; nothing was changed → commit it (the commits are archived), or remove it, then reset again", l.Name, strings.Join(dirty, ", "))
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	stamp := now.UTC().Format("20060102-150405")
	var out []ResetResult
	for _, t := range targets {
		owner := run.In(filepath.Join(m.WS.Owner, m.relOf(t.r)))
		ref := fmt.Sprintf("refs/aw/archive/%s/%s", l.Name, stamp)
		for n := 2; ok(owner.OK(ctx, "rev-parse", "--verify", "--quiet", ref)); n++ {
			ref = fmt.Sprintf("refs/aw/archive/%s/%s-%d", l.Name, stamp, n)
		}
		if _, err := owner.Run(ctx, "update-ref", ref, t.head, ""); err != nil {
			return out, fmt.Errorf("archiving %s: %w; it was not reset", repoLabel(t.r), err)
		}
		g := run.In(t.r.Path)
		if _, err := g.Run(ctx, "reset", "--quiet", "--keep", t.base); err != nil {
			return out, fmt.Errorf("%s was archived at %s, but resetting it failed: %w", repoLabel(t.r), ref, err)
		}
		if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetBaseSHA(tx, l.Number, t.r.Project, t.base) }); err != nil {
			return out, err
		}
		out = append(out, ResetResult{Repo: repoKey(t.r), Branch: t.r.Branch, Commits: t.own, Archived: ref,
			From: t.head, To: t.base, Base: shortRef(t.r.BaseRef)})
	}
	return out, nil
}
