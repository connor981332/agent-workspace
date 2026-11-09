package lanes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/forge"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/timing"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Manager claims and lays out lanes for one workspace.
type Manager struct {
	WS    *workspace.Workspace
	Cfg   *config.Config
	Store *state.Store
	// Seats takes resources for a lane's teardown, and returns how to give them back.
	Seats func(ctx context.Context, l state.Lane, needs []string) (func(), error)

	forges        map[string]*forge.Client // by host, for one sweep
	forgeDeadline time.Time
}

// Identity is the session asking, when it is known.
type Identity struct {
	Harness string
	Session string
	PID     int
}

func (id Identity) known() bool { return id.Harness != "" && id.Session != "" }

// ClaimRequest is aw attach or aw new.
type ClaimRequest struct {
	Purpose string
	Branch  string // an existing branch to start on (--branch), instead of a new one
	Who     Identity
}

// Claimed is the result of a claim.
type Claimed struct {
	Lane     state.Lane       `json:"lane"`
	Dir      string           `json:"dir"`
	Root     string           `json:"root"`
	Repos    []state.LaneRepo `json:"repos"`
	Reused   bool             `json:"reused"`    // the session already had this lane
	Returned bool             `json:"returned"`  // its processes had all ended, and it came back
	Joined   bool             `json:"joined"`    // this process joined the session (a resumed harness)
	FromPool bool             `json:"from_pool"` // a warm lane was claimed
	// Setup: the lane has setup steps, and the caller starts them in the background.
	Setup bool `json:"setup"`
}

// repoPlan is one repository a lane gets a worktree of.
type repoPlan struct {
	project string // "" for the workspace repository
	owner   string // the owner's checkout of it
	rel     string // directory relative to the owner checkout ("." for the workspace repository)
	path    string // the lane's worktree
	branch  string
	baseRef string
	baseSHA string
	track   string // remote branch to track, for --branch
	secrets []string
	// lacksBranch: --branch named a branch this repository does not have.
	lacksBranch bool
}

// Root returns the lane root of lane n.
func (m *Manager) Root(n int) string {
	return filepath.Join(m.WS.Owner, ".worktrees", Dir(n, m.Cfg.Lanes.Max))
}

// Claim claims a lane for the request: the session's own lane if it has one, else a warm lane from
// the pool, else a new one. Git work happens outside any lock: the lane is reserved in one
// transaction, built, then recorded in another.
func (m *Manager) Claim(ctx context.Context, req ClaimRequest) (*Claimed, error) {
	t := timing.Start()
	if timing.Debug() {
		defer t.Report(os.Stderr, "claim")
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	t.Mark("authorize")
	purpose := strings.TrimSpace(req.Purpose)
	if purpose == "" {
		return nil, errors.New(`a lane needs a purpose → say what the work is, e.g. aw attach "fix checkout tax rounding"`)
	}
	if req.Who.known() {
		if c, err := m.existing(ctx, req.Who); err != nil || c != nil {
			return c, err
		}
	}
	// Everything that can refuse runs before anything is reserved or created.
	plans, err := m.plan(ctx, req)
	if err != nil {
		return nil, err
	}
	t.Mark("plan")

	var lane state.Lane
	var fromPool, reused bool
	err = m.Store.Write(ctx, func(tx *sql.Tx) error {
		if req.Who.known() {
			s, err := state.SessionByHarnessID(tx, req.Who.Harness, req.Who.Session)
			if err == nil && s.Lane > 0 && s.State != state.SessionEnded {
				if l, err := state.LaneByNumber(tx, s.Lane); err == nil && l.Generation == s.LaneGeneration {
					lane, reused = l, true
					return nil
				}
			}
		}
		lanes, err := state.Lanes(tx)
		if err != nil {
			return err
		}
		taken := func(n string) bool {
			return slices.ContainsFunc(lanes, func(l state.Lane) bool { return l.Name == n })
		}
		name := Unique(Name(purpose), taken)
		now := state.Now()
		for _, l := range lanes {
			if l.State == state.LanePool {
				lane, fromPool = l, true
				break
			}
		}
		if fromPool {
			lane.Name, lane.Purpose, lane.State = name, purpose, state.LaneClaiming
			lane.Generation++
			lane.Claimed, lane.Updated, lane.Pooled = now, now, ""
			if err := state.UpdateLane(tx, lane); err != nil {
				return err
			}
		} else {
			if len(lanes) >= m.Cfg.Lanes.Max {
				return maxLanesError(m.Cfg.Lanes.Max, lanes)
			}
			n := 1
			for slices.ContainsFunc(lanes, func(l state.Lane) bool { return l.Number == n }) {
				n++
			}
			lane = state.Lane{Number: n, Name: name, Purpose: purpose, State: state.LaneClaiming, Generation: 1,
				Created: now, Claimed: now, Updated: now}
			if err := state.InsertLane(tx, lane); err != nil {
				return err
			}
		}
		return m.attach(tx, req.Who, lane, purpose)
	})
	if err != nil {
		return nil, err
	}
	if reused {
		return m.waitBuilt(ctx, lane.Number, true)
	}

	t.Mark("reserve")
	repos, buildErr := m.build(ctx, lane, plans, fromPool)
	t.Mark("build")
	if buildErr != nil {
		if rbErr := m.rollback(ctx, lane, repos, fromPool, req.Who); rbErr != nil {
			return nil, fmt.Errorf("%w; undoing it also failed (%v), so lane %s is left incomplete and protected → fix the cause, then run aw discard %s",
				buildErr, rbErr, Dir(lane.Number, m.Cfg.Lanes.Max), lane.Name)
		}
		return nil, buildErr
	}
	err = m.Store.Write(ctx, func(tx *sql.Tx) error {
		if err := state.SetLaneRepos(tx, lane.Number, repos); err != nil {
			return err
		}
		lane.State, lane.Updated = state.LaneWorking, state.Now()
		if steps := lifecycle.Plan(m.Cfg, fromPool); len(steps) > 0 {
			lane.State = state.LaneSettingUp
			if err := state.PlanSetup(tx, lane.Number, lane.Generation, fromPool, lifecycle.Names(steps)); err != nil {
				return err
			}
		}
		return state.UpdateLane(tx, lane)
	})
	if err != nil {
		return nil, err
	}
	t.Mark("record")
	compose.CopyPersonal(m.WS.Owner, m.Root(lane.Number))
	return &Claimed{Lane: lane, Dir: Dir(lane.Number, m.Cfg.Lanes.Max), Root: m.Root(lane.Number), Repos: repos, FromPool: fromPool,
		Setup: lane.State == state.LaneSettingUp}, nil
}

// existing returns the lane the session already holds, waiting while another call builds it.
func (m *Manager) existing(ctx context.Context, who Identity) (*Claimed, error) {
	s, err := state.SessionByHarnessID(m.Store.DB(), who.Harness, who.Session)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (s.Lane == 0 || s.State == state.SessionEnded)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l, err := state.LaneByNumber(m.Store.DB(), s.Lane)
	if err != nil || l.Generation != s.LaneGeneration {
		return nil, nil // the lane has moved on: a new claim
	}
	c, err := m.waitBuilt(ctx, l.Number, true)
	if err != nil {
		return nil, err
	}
	c.Returned = s.State == state.SessionInterrupted
	if who.PID <= 0 {
		return c, nil
	}
	start, err := platform.ProcessIdentity(who.PID)
	if err != nil {
		return c, nil
	}
	c.Joined = !s.Has(who.PID, start)
	if c.Joined || c.Returned {
		err = m.Store.Write(ctx, func(tx *sql.Tx) error { return state.JoinSession(tx, s.ID, who.PID, start) })
	}
	return c, err
}

// waitBuilt waits until another call has finished building lane n, then describes it.
func (m *Manager) waitBuilt(ctx context.Context, n int, reused bool) (*Claimed, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		l, err := state.LaneByNumber(m.Store.DB(), n)
		if err != nil {
			return nil, fmt.Errorf("lane %s disappeared while it was being created → retry aw attach", Dir(n, m.Cfg.Lanes.Max))
		}
		if l.State != state.LaneClaiming {
			repos, err := state.LaneRepos(m.Store.DB(), n)
			if err != nil {
				return nil, err
			}
			return &Claimed{Lane: l, Dir: Dir(n, m.Cfg.Lanes.Max), Root: m.Root(n), Repos: repos, Reused: reused}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lane %s is still being created by another call after 2 minutes → check aw doctor, then retry", Dir(n, m.Cfg.Lanes.Max))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// attach records the session as attached to the lane.
func (m *Manager) attach(tx *sql.Tx, who Identity, lane state.Lane, purpose string) error {
	if !who.known() {
		return nil
	}
	now := state.Now()
	s := state.Session{Harness: who.Harness, HarnessSession: who.Session, Lane: lane.Number,
		LaneGeneration: lane.Generation, Purpose: purpose, State: state.SessionActive, Started: now, LastActivity: now, PID: who.PID}
	if who.PID > 0 {
		s.PIDStart, _ = platform.ProcessIdentity(who.PID)
	}
	_, err := state.SaveSession(tx, s)
	return err
}

func maxLanesError(max int, lanes []state.Lane) error {
	var parked, ready []string
	for _, l := range lanes {
		switch l.State {
		case state.LaneParked:
			parked = append(parked, l.Name)
		case state.LaneReady:
			ready = append(ready, l.Name)
		}
	}
	var hints []string
	hints = append(hints, "run aw sweep to see which lanes are proven merged, then aw sweep --apply")
	if len(ready) > 0 {
		hints = append(hints, "review the lanes ready for review ("+strings.Join(ready, ", ")+")")
	}
	if len(parked) > 0 {
		hints = append(hints, "discard parked work you no longer need ("+strings.Join(parked, ", ")+") with aw discard <name>")
	}
	hints = append(hints, "or raise [lanes] max")
	return fmt.Errorf("all %d lanes are in use → %s", max, strings.Join(hints, "; "))
}

// plan works out every worktree a new lane needs and checks what can refuse: missing repositories,
// a detached owner checkout, a launcher that is not committed, and lane guards.
func (m *Manager) plan(ctx context.Context, req ClaimRequest) ([]repoPlan, error) {
	var plans []repoPlan
	single := false
	for _, name := range sortedKeys(m.Cfg.Projects) {
		if m.Cfg.Projects[name].Directory == "." {
			single = true
		}
	}
	if !single {
		p, err := m.planRepo(ctx, "", ".", req.Branch, nil)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	var missing []string
	for _, name := range sortedKeys(m.Cfg.Projects) {
		pr := m.Cfg.Projects[name]
		if pr.Lanes == "shared" {
			continue
		}
		dir := filepath.Join(m.WS.Owner, pr.Directory)
		if _, err := workspace.GitCommonDir(dir); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", name, pr.Directory))
			continue
		}
		p, err := m.planRepo(ctx, name, pr.Directory, req.Branch, pr.Secrets)
		if err != nil {
			return nil, err
		}
		if err := checkGuards(ctx, name, p, pr.LaneGuards); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no lane was created: these projects are not cloned: %s → clone each repository into its directory (or run bin/aw setup), then attach again",
			strings.Join(missing, ", "))
	}
	if req.Branch != "" && !slices.ContainsFunc(plans, func(p repoPlan) bool { return !p.lacksBranch }) {
		return nil, fmt.Errorf("no lane was created: no repository has a branch %q, locally or on origin → fetch it first (git fetch origin %s), or check the name", req.Branch, req.Branch)
	}
	// The lane root runs its own bin/aw, so the workspace repository's branch must contain it.
	root := plans[0]
	for _, f := range []string{"bin/aw", ".aw/version", ".aw/lock", "workspace.toml"} {
		if ok, _ := run.In(root.owner).OK(ctx, "cat-file", "-e", root.baseSHA+":"+f); !ok {
			return nil, fmt.Errorf("no lane was created: %s does not contain %s at %s, so a lane root would have no launcher → commit the files aw init wrote (git add workspace.toml .aw bin .gitignore && git commit), then attach again",
				label(root), f, root.baseRef)
		}
	}
	return plans, nil
}

func label(p repoPlan) string {
	if p.project == "" {
		return "the workspace repository"
	}
	return "project " + p.project
}

// planRepo decides a repository's branch and base: the owner's current branch, compared later
// with its upstream when it has one. With --branch, the lane starts on that branch.
func (m *Manager) planRepo(ctx context.Context, project, rel, branch string, secrets []string) (repoPlan, error) {
	owner := filepath.Join(m.WS.Owner, rel)
	g := run.In(owner)
	p := repoPlan{project: project, owner: owner, rel: rel, secrets: secrets}
	current, err := g.Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return p, fmt.Errorf("no lane was created: %s in %s is not on a branch (detached HEAD) → check out its main branch there", label(p), owner)
	}
	p.baseRef = "refs/heads/" + current
	if up, err := g.Line(ctx, "rev-parse", "--symbolic-full-name", current+"@{upstream}"); err == nil && up != "" {
		p.baseRef = up
	}
	start := "refs/heads/" + current
	if branch != "" {
		if ok, _ := g.OK(ctx, "check-ref-format", "--branch", branch); !ok {
			return p, fmt.Errorf("%q is not a branch name → name an existing branch", branch)
		}
		switch {
		case ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")):
			start = "refs/heads/" + branch
			p.branch = branch
		case ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch+"^{commit}")):
			start = "refs/remotes/origin/" + branch
			p.branch, p.track = branch, "origin/"+branch
		default:
			p.lacksBranch = true // this repository gets a new lane branch
		}
	}
	if p.baseSHA, err = g.Line(ctx, "rev-parse", "--verify", start+"^{commit}"); err != nil {
		return p, err
	}
	return p, nil
}

func ok(b bool, _ error) bool { return b }

// checkGuards runs a project's lane guards against the commit the lane would start from, by
// reading files with git show: no project code runs.
func checkGuards(ctx context.Context, project string, p repoPlan, guards map[string]*config.LaneGuard) error {
	for _, name := range sortedKeys(guards) {
		g := guards[name]
		content, err := run.In(p.owner).Run(ctx, "show", p.baseSHA+":"+g.File)
		if err == nil && regexp.MustCompile(g.Matches).MatchString(content) {
			continue
		}
		reason := fmt.Sprintf("%s does not match %q", g.File, g.Matches)
		if err != nil {
			reason = fmt.Sprintf("%s is missing", g.File)
		}
		what := "the lane's branch"
		if p.branch != "" {
			what = "branch " + p.branch
		}
		return fmt.Errorf("no lane was created: %s of project %s fails the lane guard %s (%s): %s", what, project, name, reason, g.Message)
	}
	return nil
}

// build creates the lane's worktrees, links, owner pointer and secret links. It returns what it
// created, so that a failure can be undone.
func (m *Manager) build(ctx context.Context, lane state.Lane, plans []repoPlan, fromPool bool) ([]state.LaneRepo, error) {
	dir := Dir(lane.Number, m.Cfg.Lanes.Max)
	root := m.Root(lane.Number)
	var made []state.LaneRepo
	for i := range plans {
		p := &plans[i]
		p.path = filepath.Join(p.owner, ".worktrees", dir)
		if p.project == "" || p.rel == "." {
			p.path = root
		}
		if p.branch == "" {
			p.branch = m.Cfg.Lanes.BranchPrefix + lane.Name
			if ok(run.In(p.owner).OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+p.branch)) {
				return made, fmt.Errorf("no lane was created: %s already has a branch %s → finish or discard the lane that used it, or delete the branch", label(*p), p.branch)
			}
		}
		g := run.In(p.owner)
		if _, err := os.Stat(p.path); err == nil && fromPool {
			// A warm lane: its worktree is clean at the base; start the new branch there.
			args := []string{"switch", "--quiet", "--no-track", "-c", p.branch, p.baseSHA}
			if p.track != "" {
				args = []string{"switch", "--quiet", "--track", "-c", p.branch, p.track}
			}
			if _, err := run.In(p.path).Run(ctx, args...); err != nil {
				return made, err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
				return made, err
			}
			args := []string{"worktree", "add", "--quiet"}
			switch {
			case p.track != "":
				args = append(args, "--track", "-b", p.branch, p.path, p.track)
			case strings.HasPrefix(p.branch, m.Cfg.Lanes.BranchPrefix) || !ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+p.branch)):
				args = append(args, "--no-track", "-b", p.branch, p.path, p.baseSHA)
			default:
				args = append(args, p.path, p.branch) // an existing local branch
			}
			if _, err := g.Run(ctx, args...); err != nil {
				return made, err
			}
		}
		made = append(made, state.LaneRepo{Lane: lane.Number, Project: p.project, Path: p.path, Branch: p.branch, BaseRef: p.baseRef, BaseSHA: p.baseSHA})
		if err := linkSecrets(ctx, *p); err != nil {
			return made, err
		}
	}
	// Links from the lane root to each project's worktree, and the owner pointer.
	for _, p := range plans {
		if p.project == "" || p.rel == "." {
			continue
		}
		link := filepath.Join(root, p.rel)
		target, err := filepath.Rel(filepath.Dir(link), p.path)
		if err != nil {
			return made, err
		}
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return made, err
		}
		if existing, err := os.Readlink(link); err == nil && existing == target {
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			return made, fmt.Errorf("linking %s to the project's worktree: %w", link, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".aw"), 0o755); err != nil {
		return made, err
	}
	if err := os.WriteFile(filepath.Join(root, workspace.OwnerPointer), []byte(m.WS.Owner+"\n"), 0o644); err != nil {
		return made, err
	}
	if err := sandbox.Exclude(m.WS.Owner); err != nil {
		return made, err
	}
	// A harness's sandbox is the user's choice; these only say what it may write if it is on.
	_, err := sandbox.Install(root, m.SandboxRoots(filepath.Base(root)))
	return made, err
}

// SandboxRoots are the paths a harness's sandbox must allow, beyond its working directory: every
// repository's Git directory, which holds aw's state and which sandboxes keep read-only, and the
// lane's worktrees, when lane is a lane directory such as "03". For the owner checkout it is "".
func (m *Manager) SandboxRoots(lane string) []string {
	roots := []string{filepath.Join(m.WS.Owner, ".git")}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		p := m.Cfg.Projects[name]
		if p.Directory != "." {
			roots = append(roots, filepath.Join(m.WS.Owner, p.Directory, ".git"))
		}
		if lane != "" && p.Lanes != "shared" {
			roots = append(roots, filepath.Join(m.WS.Owner, p.Directory, ".worktrees", lane))
		}
	}
	return roots
}

// linkSecrets links each secret file of the owner's checkout into the worktree, relatively, and
// keeps the links out of Git (R67, C2 finding F10).
func linkSecrets(ctx context.Context, p repoPlan) error {
	if len(p.secrets) == 0 {
		return nil
	}
	common, err := workspace.GitCommonDir(p.owner)
	if err != nil {
		return err
	}
	var excludes []string
	for _, sec := range p.secrets {
		src := filepath.Join(p.owner, sec)
		if _, err := os.Stat(src); err != nil {
			continue // the owner has no such file yet
		}
		link := filepath.Join(p.path, sec)
		if _, err := os.Lstat(link); err == nil {
			continue // the branch tracks a file there; never replace it
		}
		target, err := filepath.Rel(filepath.Dir(link), src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
		excludes = append(excludes, "/"+filepath.ToSlash(sec))
	}
	return addExcludes(filepath.Join(common, "info", "exclude"), excludes)
}

func addExcludes(path string, entries []string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, e := range entries {
		if !have[e] {
			add = append(add, e)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		fmt.Fprintln(f)
	}
	fmt.Fprintln(f, "# Agent Workspace: secret links in lanes")
	for _, e := range add {
		fmt.Fprintln(f, e)
	}
	return nil
}

// rollback undoes a failed build without forcing anything: new worktrees are clean, so a plain
// remove works, and new branches still point at their start.
func (m *Manager) rollback(ctx context.Context, lane state.Lane, made []state.LaneRepo, fromPool bool, who Identity) error {
	var errs []string
	if !fromPool {
		for i := len(made) - 1; i >= 0; i-- {
			r := made[i]
			owner := filepath.Join(m.WS.Owner, m.relOf(r))
			if _, err := run.In(owner).Run(ctx, "worktree", "remove", r.Path); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if strings.HasPrefix(r.Branch, m.Cfg.Lanes.BranchPrefix) {
				if _, err := run.In(owner).Run(ctx, "branch", "-d", r.Branch); err != nil {
					errs = append(errs, err.Error())
				}
			}
		}
		os.RemoveAll(filepath.Join(m.Root(lane.Number), ".aw")) // the owner pointer, if written
		os.Remove(m.Root(lane.Number))
	}
	if fromPool {
		// A warm lane keeps its worktrees: put each back at its base, without the new branch.
		for _, r := range made {
			if _, err := run.In(r.Path).Run(ctx, "switch", "--quiet", "--detach", r.BaseSHA); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if strings.HasPrefix(r.Branch, m.Cfg.Lanes.BranchPrefix) {
				if _, err := run.In(r.Path).Run(ctx, "branch", "-d", r.Branch); err != nil {
					errs = append(errs, err.Error())
				}
			}
		}
	}
	if len(errs) > 0 {
		m.Store.Write(ctx, func(tx *sql.Tx) error {
			lane.State, lane.Updated = state.LaneIncomplete, state.Now()
			if err := state.SetLaneRepos(tx, lane.Number, made); err != nil {
				return err
			}
			return state.UpdateLane(tx, lane)
		})
		return errors.New(strings.Join(errs, "; "))
	}
	return m.Store.Write(ctx, func(tx *sql.Tx) error {
		if who.known() {
			if _, err := tx.Exec(`UPDATE sessions SET lane = NULL, lane_generation = NULL WHERE harness = ? AND harness_session = ?`, who.Harness, who.Session); err != nil {
				return err
			}
		}
		if fromPool {
			lane.Name, lane.Purpose, lane.State, lane.Pooled = "", "", state.LanePool, state.Now()
			return state.UpdateLane(tx, lane)
		}
		return state.DeleteLane(tx, lane.Number)
	})
}

// relOf is a lane repository's directory in the owner checkout.
func (m *Manager) relOf(r state.LaneRepo) string {
	if r.Project == "" {
		return "."
	}
	if p, ok := m.Cfg.Projects[r.Project]; ok {
		return p.Directory
	}
	return r.Project
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
