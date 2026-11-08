package lanes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
)

// Park shelves a lane on purpose: it is protected from cleanup until it is unparked (R79). (M4
// adds releasing its leases and stopping its servers.)
func (m *Manager) Park(ctx context.Context, l state.Lane) (state.Lane, error) {
	switch l.State {
	case state.LaneParked:
		return l, nil
	case state.LanePool:
		return l, fmt.Errorf("lane %s is in the pool, with no work to park", Dir(l.Number, m.Cfg.Lanes.Max))
	case state.LaneClaiming:
		return l, fmt.Errorf("lane %s is still being created → retry in a moment", l.Name)
	}
	return m.setState(ctx, l, state.LaneParked)
}

// Unpark returns a parked lane to working.
func (m *Manager) Unpark(ctx context.Context, l state.Lane) (state.Lane, error) {
	if l.State != state.LaneParked {
		return l, fmt.Errorf("lane %s is %s, not parked", l.Name, l.State)
	}
	return m.setState(ctx, l, state.LaneWorking)
}

func (m *Manager) setState(ctx context.Context, l state.Lane, s string) (state.Lane, error) {
	err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		cur, err := state.LaneByNumber(tx, l.Number)
		if err != nil {
			return err
		}
		if cur.Generation != l.Generation {
			return fmt.Errorf("lane %s changed while this ran → run the command again", Dir(l.Number, m.Cfg.Lanes.Max))
		}
		cur.State, cur.Updated = s, state.Now()
		l = cur
		return state.UpdateLane(tx, cur)
	})
	return l, err
}

// Discard abandons a lane's unmerged work (R27): every branch is archived under
// refs/aw/archive/<name>/<time> first, then the lane is destroyed. Uncommitted work is never lost:
// a lane with any is refused.
func (m *Manager) Discard(ctx context.Context, l state.Lane, now time.Time) ([]string, error) {
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	if l.State == state.LanePool || l.State == state.LaneClaiming {
		return nil, fmt.Errorf("lane %s has no work to discard", Dir(l.Number, m.Cfg.Lanes.Max))
	}
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	var dirty []string
	for _, r := range repos {
		if _, err := os.Stat(r.Path); err != nil {
			continue
		}
		status, err := run.In(r.Path).Run(ctx, "status", "--porcelain=v1", "-uall")
		if err != nil {
			return nil, err
		}
		if status != "" {
			dirty = append(dirty, fmt.Sprintf("%s (%d)", repoLabel(r), strings.Count(status, "\n")))
		}
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("lane %s has uncommitted work in %s, which aw discard would lose; nothing was changed → commit it in the lane (the commits are archived), or remove it yourself, then discard again",
			l.Name, strings.Join(dirty, ", "))
	}
	stamp := now.UTC().Format("20060102-150405")
	var archived []string
	for _, r := range repos {
		if r.Branch == "" {
			continue
		}
		owner := filepath.Join(m.WS.Owner, m.relOf(r))
		g := run.In(owner)
		head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch)
		if err != nil {
			continue
		}
		ref := fmt.Sprintf("refs/aw/archive/%s/%s", l.Name, stamp)
		if _, err := g.Run(ctx, "update-ref", ref, head, ""); err != nil {
			return archived, fmt.Errorf("archiving %s: %w; nothing was removed", repoLabel(r), err)
		}
		archived = append(archived, fmt.Sprintf("%s %s", repoLabel(r), ref))
	}
	return archived, m.destroy(ctx, l, repos, true)
}

// FinishOptions are aw finish's arguments.
type FinishOptions struct {
	Summary string
	Plan    string // a file inside the lane
}

// Finished is what aw finish recorded.
type Finished struct {
	Lane    state.Lane        `json:"lane"`
	Heads   map[string]string `json:"heads"`
	Summary string            `json:"summary"`
	Plan    string            `json:"plan,omitempty"`
	Already bool              `json:"already"` // the same heads were already handed off
}

// Finish hands a lane off for review (R78): it records the exact head of every repository and the
// handoff note, and moves the lane to ready, where it stays protected. It refuses, listing every
// problem at once, when a repository is dirty, detached, or moves while it runs. It needs no
// network: whether the branch is pushed is the human's concern when reviewing.
func (m *Manager) Finish(ctx context.Context, l state.Lane, o FinishOptions) (*Finished, error) {
	if strings.TrimSpace(o.Summary) == "" {
		return nil, fmt.Errorf(`aw finish needs a summary of the work → aw finish --summary "what changed, and what to verify"`)
	}
	switch l.State {
	case state.LaneWorking, state.LaneReady, state.LaneParked:
	default:
		return nil, fmt.Errorf("lane %s is %s, so it cannot be finished", Dir(l.Number, m.Cfg.Lanes.Max), l.State)
	}
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	root := m.Root(l.Number)
	plan := ""
	var problems []string
	if o.Plan != "" {
		if plan, err = planInLane(root, o.Plan); err != nil {
			problems = append(problems, err.Error())
		}
	}
	readHeads := func() (map[string]string, []string) {
		heads := map[string]string{}
		var probs []string
		for _, r := range repos {
			g := run.In(r.Path)
			branch, err := g.Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
			switch {
			case err != nil:
				probs = append(probs, repoLabel(r)+" is on a detached HEAD → check out the lane's branch "+r.Branch)
				continue
			case branch != r.Branch:
				probs = append(probs, fmt.Sprintf("%s is on %s, not the lane's branch %s → switch back to %s", repoLabel(r), branch, r.Branch, r.Branch))
				continue
			}
			if status, err := g.Run(ctx, "status", "--porcelain=v1", "-uall"); err != nil {
				probs = append(probs, repoLabel(r)+": "+err.Error())
			} else if status != "" {
				probs = append(probs, fmt.Sprintf("%s has %d uncommitted changes → commit or remove them", repoLabel(r), strings.Count(status, "\n")))
			}
			head, err := g.Line(ctx, "rev-parse", "--verify", "HEAD^{commit}")
			if err != nil {
				probs = append(probs, repoLabel(r)+": its head cannot be read")
				continue
			}
			heads[repoKey(r)] = head
		}
		return heads, probs
	}
	heads, probs := readHeads()
	problems = append(problems, probs...)
	if len(problems) > 0 {
		return nil, fmt.Errorf("lane %s was not finished:\n  %s", l.Name, strings.Join(problems, "\n  "))
	}

	if h, err := state.LatestHandoff(m.Store.DB(), l.Number, l.Generation); err == nil && l.State == state.LaneReady {
		var old map[string]string
		if json.Unmarshal([]byte(h.Heads), &old) == nil && sameHeads(old, heads) && h.Summary == o.Summary && h.Plan == plan {
			return &Finished{Lane: l, Heads: heads, Summary: h.Summary, Plan: h.Plan, Already: true}, nil
		}
	}
	// The heads must not move while the handoff is written: read them again under the lock.
	data, _ := json.Marshal(heads)
	err = m.Store.Write(ctx, func(tx *sql.Tx) error {
		again, probs := readHeads()
		if len(probs) > 0 || !sameHeads(again, heads) {
			return fmt.Errorf("lane %s was not finished: a repository changed while aw finish ran → finish again when the work is settled", l.Name)
		}
		cur, err := state.LaneByNumber(tx, l.Number)
		if err != nil || cur.Generation != l.Generation {
			return fmt.Errorf("lane %s changed while aw finish ran → run it again", Dir(l.Number, m.Cfg.Lanes.Max))
		}
		if err := state.AddHandoff(tx, state.Handoff{Lane: l.Number, Generation: l.Generation, Summary: o.Summary, Plan: plan, Heads: string(data)}); err != nil {
			return err
		}
		cur.State, cur.Updated = state.LaneReady, state.Now()
		l = cur
		return state.UpdateLane(tx, cur)
	})
	if err != nil {
		return nil, err
	}
	return &Finished{Lane: l, Heads: heads, Summary: o.Summary, Plan: plan}, nil
}

func repoKey(r state.LaneRepo) string {
	if r.Project == "" {
		return "."
	}
	return r.Project
}

func sameHeads(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// planInLane resolves a plan path relative to the lane root, following symlinks, and refuses one
// that leaves the lane.
func planInLane(root, plan string) (string, error) {
	p := plan
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("the plan %s cannot be read → give a file inside the lane", plan)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("the plan %s is outside the lane (it resolves to %s) → give a file inside the lane", plan, real)
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("the plan %s is not a file", plan)
	}
	return rel, nil
}
