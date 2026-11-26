package lanes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Lazy worktrees (docs/spec/sessions.md, "Lazy worktrees"): a lane checks a project out on first
// use, as it sets one up on first use. A claim starts with the eager projects and those it names.

// laneProject reports whether a project gets a worktree of its own in a lane: not the workspace
// itself (directory "."), whose worktree is the lane root, and not a shared project.
func (m *Manager) laneProject(name string) bool {
	p := m.Cfg.Projects[name]
	return p != nil && p.Directory != "." && p.Lanes != "shared"
}

// checkProjects refuses a name that is no project, or one lanes do not check out.
func (m *Manager) checkProjects(names []string) error {
	for _, n := range names {
		p, ok := m.Cfg.Projects[n]
		switch {
		case !ok:
			return fmt.Errorf("there is no project named %q → known: %s", n, strings.Join(sortedKeys(m.Cfg.Projects), ", "))
		case p.Lanes == "shared":
			return fmt.Errorf("project %s is shared: lanes use the owner's checkout of it, so there is nothing to check out", n)
		}
	}
	return nil
}

// WithDependencies is names and every lane project they depend_on, recursively, in name order.
func (m *Manager) WithDependencies(names []string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] || m.Cfg.Projects[n] == nil {
			return
		}
		seen[n] = true
		for _, d := range m.Cfg.Projects[n].DependsOn {
			visit(d)
		}
	}
	for _, n := range names {
		visit(n)
	}
	var out []string
	for _, n := range sortedKeys(seen) {
		if m.laneProject(n) {
			out = append(out, n)
		}
	}
	return out
}

// StartProjects are the projects a new lane checks out: every one with [lanes] setup = "eager",
// otherwise those in [lanes] eager and the ones named, with what they depend on.
func (m *Manager) StartProjects(named []string) []string {
	if !lifecycle.Lazy(m.Cfg) {
		var all []string
		for _, n := range sortedKeys(m.Cfg.Projects) {
			if m.laneProject(n) {
				all = append(all, n)
			}
		}
		return all
	}
	return m.WithDependencies(slices.Concat(m.Cfg.Lanes.Eager, named))
}

// presentPlans plans the projects a warm lane already has a worktree of, beyond those planned: the
// claim puts them on the new lane's branch too, since a worktree in the lane that is not on its
// branch would hold work aw cannot see.
func (m *Manager) presentPlans(ctx context.Context, lane state.Lane, plans []repoPlan, branch string) ([]repoPlan, error) {
	dir := Dir(lane.Number, m.Cfg.Lanes.Max)
	var out []repoPlan
	for _, name := range sortedKeys(m.Cfg.Projects) {
		pr := m.Cfg.Projects[name]
		if !m.laneProject(name) || slices.ContainsFunc(plans, func(p repoPlan) bool { return p.project == name }) {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.WS.Owner, pr.Directory, ".worktrees", dir)); err != nil {
			continue
		}
		p, err := m.planRepo(ctx, name, pr.Directory, branch, pr.Secrets)
		if err != nil {
			return nil, err
		}
		p.localFiles = pr.LocalFiles
		out = append(out, p)
	}
	return out, nil
}

// Used is what Use did.
type Used struct {
	Lane  state.Lane `json:"lane"`
	Added []string   `json:"added"` // the projects checked out now, dependencies included
	Had   []string   `json:"had"`   // the ones asked for that the lane already had
}

// Use checks projects out in lane n, with the projects they depend on: their worktrees on the
// lane's branch, secret links, local files and links from the lane root. Their setup steps are
// added to the lane's plan; the caller runs the ones that run at claim time (their prepare), and
// the rest wait for first use. It is idempotent.
func (m *Manager) Use(ctx context.Context, n int, projects []string) (*Used, error) {
	if err := m.checkProjects(projects); err != nil {
		return nil, err
	}
	// Two first uses at once, such as two aw run commands, would both create the worktree, and the
	// loser's undo would remove the winner's: the second waits, then finds it checked out.
	unlock, err := m.useLock(ctx, n)
	if err != nil {
		return nil, err
	}
	defer unlock()
	lane, err := state.LaneByNumber(m.Store.DB(), n)
	if err != nil {
		return nil, fmt.Errorf("there is no lane %d → run aw to list the lanes", n)
	}
	switch lane.State {
	case state.LanePool, state.LaneClaiming:
		return nil, fmt.Errorf("lane %s is not claimed yet → attach to it first (aw attach)", Dir(n, m.Cfg.Lanes.Max))
	}
	repos, err := state.LaneRepos(m.Store.DB(), n)
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	branch := ""
	for _, r := range repos {
		has[r.Project] = true
		if r.Branch != "" && !strings.HasPrefix(r.Branch, m.Cfg.Lanes.BranchPrefix) {
			branch = r.Branch // the lane started on a branch of its own (--branch): use it where it exists
		}
	}
	u := &Used{Lane: lane, Added: []string{}, Had: []string{}}
	for _, p := range projects {
		if has[p] || !m.laneProject(p) {
			u.Had = append(u.Had, p)
		}
	}
	var plans []repoPlan
	var missing []string
	for _, name := range m.WithDependencies(projects) {
		if has[name] {
			continue
		}
		pr := m.Cfg.Projects[name]
		if _, err := workspace.GitCommonDir(filepath.Join(m.WS.Owner, pr.Directory)); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", name, pr.Directory))
			continue
		}
		p, err := m.planRepo(ctx, name, pr.Directory, branch, pr.Secrets)
		if err != nil {
			return nil, notCheckedOut(err)
		}
		p.localFiles = pr.LocalFiles
		if err := checkGuards(ctx, name, p, pr.LaneGuards); err != nil {
			return nil, notCheckedOut(err)
		}
		plans = append(plans, p)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("these projects are not cloned: %s → ask the human to clone each into its directory (bin/aw setup does it)", strings.Join(missing, ", "))
	}
	if len(plans) == 0 {
		return u, nil
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	made, err := m.buildRepos(ctx, lane, plans, false)
	if err == nil {
		err = linkProjects(m.Root(n), plans)
	}
	if err != nil {
		if rbErr := m.undoRepos(ctx, n, made); rbErr != nil {
			return nil, fmt.Errorf("%w; undoing it also failed (%v) → fix the cause, then run bin/aw use again", err, rbErr)
		}
		return nil, err
	}
	hooks := lifecycle.CheckoutHooks(ctx, m.WS.Owner, m.Cfg)
	err = m.Store.Write(ctx, func(tx *sql.Tx) error {
		all, err := state.LaneRepos(tx, n)
		if err != nil {
			return err
		}
		if err := state.SetLaneRepos(tx, n, append(all, made...)); err != nil {
			return err
		}
		var have, recorded []string
		for _, r := range append(all, made...) {
			if r.Project != "" {
				have = append(have, r.Project)
			}
		}
		steps, err := state.Steps(tx, n, lane.Generation)
		if err != nil {
			return err
		}
		for _, s := range steps {
			recorded = append(recorded, s.Name)
		}
		for _, p := range plans {
			recorded = append(recorded, lifecycle.FreshSteps(m.Cfg, p.project)...) // a new worktree is set up, not reset
		}
		setup, err := state.LaneSetup(tx, n)
		if errors.Is(err, state.ErrNotFound) {
			// A lane whose claim had no steps: these are its first.
			names := lifecycle.Names(lifecycle.LanePlan(m.Cfg, false, hooks, have, nil))
			if len(names) == 0 {
				return nil
			}
			return state.PlanSetup(tx, n, lane.Generation, false, names)
		} else if err != nil {
			return err
		}
		fromPool := setup.FromPool && setup.Generation == lane.Generation
		return state.AddSetupSteps(tx, n, lane.Generation, lifecycle.Names(lifecycle.LanePlan(m.Cfg, fromPool, hooks, have, recorded)))
	})
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		u.Added = append(u.Added, p.project)
	}
	return u, nil
}

// notCheckedOut words a planning refusal, which a claim prefixes with "no lane was created", for a
// lane that exists: what blocks the project is kept, and the lane is untouched.
func notCheckedOut(err error) error {
	const claimPrefix = "no lane was created: "
	if msg := err.Error(); strings.HasPrefix(msg, claimPrefix) {
		return fmt.Errorf("nothing was checked out: %s", strings.TrimPrefix(msg, claimPrefix))
	}
	return err
}

// undoRepos removes the worktrees Use created before it failed, their links and the branches it made
// for them, as a failed claim's rollback does.
func (m *Manager) undoRepos(ctx context.Context, n int, made []state.LaneRepo) error {
	var errs []string
	for i := len(made) - 1; i >= 0; i-- {
		r := made[i]
		owner := filepath.Join(m.WS.Owner, m.relOf(r))
		g := run.In(owner, noHooks...)
		if _, err := os.Lstat(filepath.Join(r.Path, ".git")); err == nil {
			if _, err := worktreeGit(ctx, g, "", "worktree", "remove", "--force", r.Path); err != nil {
				errs = append(errs, err.Error())
				continue
			}
		} else if _, err := worktreeGit(ctx, g, "", "worktree", "prune"); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		os.Remove(filepath.Join(m.Root(n), filepath.FromSlash(m.relOf(r)))) // the link, if it was made
		if strings.HasPrefix(r.Branch, m.Cfg.Lanes.BranchPrefix) && ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch)) {
			if _, err := g.Run(ctx, "branch", "-D", r.Branch); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// useLock takes lane n's checkout lock. No state lock is held while it waits.
func (m *Manager) useLock(ctx context.Context, n int) (func(), error) {
	dir, err := m.StateDir(n)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "use.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }()
	select {
	case err := <-done:
		if err != nil {
			f.Close()
			return nil, err
		}
		return func() { f.Close() }, nil
	case <-ctx.Done():
		go func() { <-done; f.Close() }()
		return nil, ctx.Err()
	}
}
