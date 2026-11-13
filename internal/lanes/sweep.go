package lanes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/evidence"
	"github.com/connor981332/agent-workspace/internal/forge"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/providers"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Sweep actions.
const (
	ActionKeep    = "keep"
	ActionRecycle = "recycle" // reset to the base and returned to the pool
	ActionDestroy = "destroy" // removed: beyond the pool size, or past retention
	ActionPooled  = "pooled"  // a warm lane that stays in the pool
)

// Decision is what sweep does with one lane, and why.
type Decision struct {
	Number int              `json:"number"`
	Dir    string           `json:"lane"`
	Name   string           `json:"name"`
	State  string           `json:"state"`
	Action string           `json:"action"`
	Reason string           `json:"reason"`
	Repos  []evidence.Repo  `json:"repos,omitempty"`
	Next   []string         `json:"next,omitempty"`   // for a kept lane, what would let it be recycled
	Result string           `json:"result,omitempty"` // after --apply: done, skipped, failed
	Error  string           `json:"error,omitempty"`
	lane   state.Lane       `json:"-"`
	stored []state.LaneRepo `json:"-"`
}

// SweepReport is the result of aw sweep.
type SweepReport struct {
	Applied bool                 `json:"applied"`
	Lanes   []*Decision          `json:"lanes"`
	Open    []state.JournalEntry `json:"unfinished"` // cleanups that failed earlier
}

// SweepOptions configure a sweep.
type SweepOptions struct {
	Apply  bool
	Caller int // the lane the command runs in, which is never swept
	Retry  int // a journal entry to retry
	Now    time.Time
}

// Sweep decides, for every lane, whether it is kept, recycled or destroyed. With Apply, it carries
// the decisions out, re-checking each lane immediately before acting on it.
func (m *Manager) Sweep(ctx context.Context, o SweepOptions) (*SweepReport, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	lanes, err := state.Lanes(m.Store.DB())
	if err != nil {
		return nil, err
	}
	sessions, err := state.Sessions(m.Store.DB())
	if err != nil {
		return nil, err
	}
	open, err := state.OpenJournal(m.Store.DB())
	if err != nil {
		return nil, err
	}
	rep := &SweepReport{Applied: o.Apply, Open: open}
	if rep.Open == nil {
		rep.Open = []state.JournalEntry{}
	}
	failedLanes := map[int]state.JournalEntry{}
	for _, e := range open {
		failedLanes[e.Lane] = e
	}

	m.forges, m.forgeDeadline = map[string]*forge.Client{}, o.Now.Add(30*time.Second)
	pooled := 0
	for _, l := range lanes {
		d := &Decision{Number: l.Number, Dir: Dir(l.Number, m.Cfg.Lanes.Max), Name: l.Name, State: l.State, lane: l}
		rep.Lanes = append(rep.Lanes, d)
		if e, ok := failedLanes[l.Number]; ok && o.Retry != e.ID {
			d.Action, d.Reason = ActionKeep, fmt.Sprintf("an earlier %s failed (%s) → fix the cause, then aw sweep --apply --retry %d", e.Kind, e.Detail, e.ID)
			continue
		}
		if l.State == state.LanePool {
			continue // decided below, once the recyclable lanes are known
		}
		m.decide(ctx, d, sessions, o.Caller)
	}

	// The pool keeps [lanes] pool lanes; the rest, and lanes past retention, are destroyed. A
	// warm lane whose age cannot be read is kept.
	retention := time.Duration(m.Cfg.Lanes.Retention)
	for _, d := range rep.Lanes {
		if d.lane.State != state.LanePool || d.Action != "" {
			continue
		}
		pooledAt, err := time.Parse(time.RFC3339Nano, d.lane.Pooled)
		switch {
		case err != nil:
			d.Action, d.Reason = ActionKeep, "its age in the pool is unknown, so it is kept"
		case retention > 0 && o.Now.Sub(pooledAt) > retention:
			d.Action, d.Reason = ActionDestroy, fmt.Sprintf("in the pool for more than %s ([lanes] retention)", retention)
		case pooled >= m.Cfg.Lanes.Pool:
			d.Action, d.Reason = ActionDestroy, fmt.Sprintf("the pool already has %d lanes ([lanes] pool)", m.Cfg.Lanes.Pool)
		default:
			d.Action, d.Reason = ActionPooled, "warm, ready to be claimed"
			pooled++
		}
	}
	for _, d := range rep.Lanes {
		if d.Action == ActionRecycle && pooled >= m.Cfg.Lanes.Pool {
			d.Action, d.Reason = ActionDestroy, d.Reason+"; the pool is full ([lanes] pool), so it is removed"
		} else if d.Action == ActionRecycle {
			pooled++
		}
	}

	if !o.Apply {
		return rep, nil
	}
	for _, d := range rep.Lanes {
		if d.Action != ActionRecycle && d.Action != ActionDestroy {
			continue
		}
		if err := m.apply(ctx, d, o); err != nil {
			d.Result, d.Error = "failed", err.Error()
		}
	}
	rep.Open, _ = state.OpenJournal(m.Store.DB())
	if rep.Open == nil {
		rep.Open = []state.JournalEntry{}
	}
	return rep, nil
}

// decide examines a lane that is not in the pool. Anything that cannot be read keeps the lane.
func firstNote(notes []state.Note) string {
	if len(notes) == 0 {
		return ""
	}
	return notes[0].Text
}

func (m *Manager) decide(ctx context.Context, d *Decision, sessions []state.Session, caller int) {
	keep := func(format string, args ...any) { d.Action, d.Reason = ActionKeep, fmt.Sprintf(format, args...) }
	l := d.lane
	switch l.State {
	case state.LaneClaiming, state.LaneSettingUp:
		keep("it is being set up")
		return
	case state.LaneIncomplete:
		keep("its setup is incomplete")
		return
	case state.LaneParked:
		keep("parked")
		return
	}
	if l.Number == caller {
		keep("this command runs in it")
		return
	}
	for _, s := range sessions {
		if s.Lane == l.Number && s.LaneGeneration == l.Generation && s.State == state.SessionActive && Live(s) {
			keep("session %s (%s) is attached", s.HarnessSession, s.Harness)
			return
		}
	}
	// An open note is work the lane's agent left outside it, which no merge proves done.
	if notes, err := state.OpenNotes(m.Store.DB(), l.Number, l.Generation); err != nil || len(notes) > 0 {
		keep("it has %d open note%s, such as %q → do them, or mark them done with aw note --done <n> in the lane", len(notes), plural(len(notes)), firstNote(notes))
		return
	}
	root := m.Root(l.Number)
	if info, err := os.Lstat(root); err != nil {
		keep("its directory %s cannot be read: %v", root, err)
		return
	} else if info.Mode()&os.ModeSymlink != 0 {
		keep("its directory %s is a symlink, not the lane aw made", root)
		return
	}
	if data, err := os.ReadFile(filepath.Join(root, ".aw", "owner")); err != nil || strings.TrimSpace(string(data)) != m.WS.Owner {
		keep("its lane marker (.aw/owner) does not name this owner checkout")
		return
	}
	stored, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil || len(stored) == 0 {
		keep("its repositories cannot be read from the state")
		return
	}
	d.stored = stored
	var reasons []string
	for _, r := range stored {
		owner := filepath.Join(m.WS.Owner, m.relOf(r))
		if locked, err := worktreeLocked(ctx, owner, r.Path); err != nil || locked {
			keep("%s's worktree is locked (git worktree lock), or its lock cannot be read", repoLabel(r))
			return
		}
		ev := evidence.Check(ctx, r.Path, r.BaseRef, m.evidenceOptions(r))
		if !ev.Unknown && ev.Clean && ev.Proof == "" {
			m.forgeProof(ctx, owner, r, &ev)
		}
		d.Repos = append(d.Repos, ev)
		switch {
		case ev.Unknown:
			keep("%s: %s, so it is protected", repoLabel(r), ev.Reason)
			return
		case !ev.Clean:
			reasons = append(reasons, repoLabel(r)+": "+ev.Dirty)
			d.Next = append(d.Next, cleanStep(r, ev))
		case ev.Proof == "":
			reasons = append(reasons, repoLabel(r)+": "+ev.Reason)
			d.Next = append(d.Next, fmt.Sprintf("%s: merge its work (aw changeset opens the merge requests), or aw resume %s to keep working on it", repoLabel(r), l.Name))
		}
	}
	if len(reasons) > 0 {
		prefix := ""
		if l.State == state.LaneReady {
			prefix = "ready for review, but "
		}
		keep("%s%s", prefix, strings.Join(reasons, "; "))
		return
	}
	d.Action, d.Reason = ActionRecycle, "every repository is clean and proven merged"
}

// cleanStep says how to make a repository clean enough to recycle.
func cleanStep(r state.LaneRepo, ev evidence.Repo) string {
	if len(ev.Ignored) == 0 {
		return fmt.Sprintf("%s: commit or discard the changes in %s", repoLabel(r), r.Path)
	}
	shown := ev.Ignored
	if len(shown) > 3 {
		shown = append(slices.Clone(shown[:3]), "…")
	}
	section := "[workspace]"
	if r.Project != "" {
		section = fmt.Sprintf("[projects.%s]", r.Project)
	}
	it := "it"
	if len(ev.Ignored) > 1 {
		it = "them"
	}
	return fmt.Sprintf("%s: if builds make %s, add %s to generated under %s in workspace.toml; if not, delete %s from %s",
		repoLabel(r), strings.Join(shown, ", "), it, section, it, r.Path)
}

// forgeProof asks the repository's forge whether exactly this head was merged (lifecycle.md, merge
// proof 3). It never proves anything on an error.
func (m *Manager) forgeProof(ctx context.Context, owner string, r state.LaneRepo, ev *evidence.Repo) {
	remote, target := "origin", strings.TrimPrefix(r.BaseRef, "refs/heads/")
	if rest, ok := strings.CutPrefix(r.BaseRef, "refs/remotes/"); ok {
		remote, target, _ = strings.Cut(rest, "/")
	}
	raw, err := run.In(owner).Line(ctx, "config", "--get", "remote."+remote+".url")
	if err != nil {
		return
	}
	host, project, ok := forge.ParseRemote(raw)
	if !ok {
		return
	}
	c, seen := m.forges[host]
	if !seen {
		c = forge.For(m.Cfg.Workspace.Forge, host, m.forgeDeadline)
		m.forges[host] = c
	}
	if c == nil {
		return
	}
	ch := c.Find(ctx, forge.Identity{Host: host, Project: project, Target: target}, r.RemoteBranch(), ev.Head)
	ev.Forge = &ch
	if ch.Merged() {
		ev.Proof, ev.Reason = evidence.Forge, ""
		return
	}
	what := ch.Availability
	if ch.Availability == forge.Verified {
		what = fmt.Sprintf("change !%d is %s", ch.IID, ch.State)
	}
	ev.Reason += fmt.Sprintf(" (%s: %s)", c.Kind(), what)
}

func repoLabel(r state.LaneRepo) string {
	if r.Project == "" {
		return "workspace repository"
	}
	return r.Project
}

// evidenceOptions lists what may be ignored in a lane repository without blocking: the project's
// generated paths, its secret links, and in the lane root the workspace's generated paths, the project
// links and .aw/owner.
func (m *Manager) evidenceOptions(r state.LaneRepo) evidence.Options {
	o := evidence.Options{ExpectBranch: r.Branch}
	if r.Project == "" {
		o.Generated = append(o.Generated, m.Cfg.Workspace.Generated...)
		o.Ours = append(o.Ours, append([]string{".aw/owner", workspace.MaterializedPath}, sandbox.Ours(r.Path)...)...)
		for _, name := range sortedKeys(m.Cfg.Projects) {
			if p := m.Cfg.Projects[name]; p.Directory != "." && p.Lanes != "shared" {
				o.Ours = append(o.Ours, p.Directory)
			}
		}
		return o
	}
	if p, ok := m.Cfg.Projects[r.Project]; ok {
		o.Generated = append(o.Generated, p.Generated...)
		o.Ours = append(o.Ours, p.Secrets...)
		o.Ours = append(o.Ours, p.LocalFiles...)
		if p.Directory == "." {
			o.Ours = append(o.Ours, append([]string{".aw/owner", workspace.MaterializedPath}, sandbox.Ours(r.Path)...)...)
		}
	}
	return o
}

func worktreeLocked(ctx context.Context, owner, path string) (bool, error) {
	out, err := worktreeGit(ctx, run.In(owner), "", "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	var current string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			current = p
		}
		if strings.HasPrefix(line, "locked") && sameFile(current, path) {
			return true, nil
		}
	}
	return false, nil
}

func sameFile(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

// apply carries out one decision, re-checking first, and journals it.
func (m *Manager) apply(ctx context.Context, d *Decision, o SweepOptions) error {
	if d.lane.State != state.LanePool {
		before := d.fingerprint()
		recheck := &Decision{lane: d.lane, Number: d.Number}
		if l, err := state.LaneByNumber(m.Store.DB(), d.Number); err != nil || l.Generation != d.lane.Generation || l.State != d.lane.State {
			d.Result, d.Reason = "skipped", "it changed since it was checked"
			return nil
		}
		current, _ := state.Sessions(m.Store.DB())
		m.decide(ctx, recheck, current, o.Caller)
		if recheck.Action != ActionRecycle || recheck.fingerprint() != before {
			d.Result, d.Reason = "skipped", "it changed since it was checked: "+recheck.Reason
			return nil
		}
	}
	var jid int
	if err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		if o.Retry > 0 {
			if e, err := state.JournalByID(tx, o.Retry); err == nil && e.Lane == d.Number {
				if err := state.EndJournal(tx, e.ID, state.JournalDone, "retried as a new entry"); err != nil {
					return err
				}
			}
		}
		var err error
		jid, err = state.StartJournal(tx, d.Action, d.Number, "start")
		return err
	}); err != nil {
		return err
	}
	var err error
	if d.Action == ActionRecycle {
		err = m.recycle(ctx, d)
	} else {
		err = m.destroy(ctx, d.lane, d.stored, false)
	}
	status, detail := state.JournalDone, ""
	if err != nil {
		status, detail = state.JournalFailed, err.Error()
	}
	if werr := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.EndJournal(tx, jid, status, detail) }); werr != nil && err == nil {
		err = werr
	}
	if err == nil {
		d.Result = "done"
	} else {
		err = fmt.Errorf("%w → fix the cause, then aw sweep --apply --retry %d", err, jid)
	}
	return err
}

func (d *Decision) fingerprint() string {
	var parts []string
	for _, r := range d.Repos {
		parts = append(parts, r.Path+"@"+r.Head+"@"+r.Base+"@"+r.Branch)
	}
	return strings.Join(parts, "|")
}

// recycle resets every worktree to its base, deletes the lane's branches, and returns the lane to
// the pool under its number. Untracked caches stay: stable paths keep them valid across reuse.
func (m *Manager) recycle(ctx context.Context, d *Decision) error {
	if err := m.authorizeRefs(ctx); err != nil {
		return err
	}
	var repos []state.LaneRepo
	for i, r := range d.stored {
		ev := d.Repos[i]
		g := run.In(r.Path)
		removeMaterialized(r.Path) // a copy that replaced a tracked file must not travel to the base
		if _, err := g.Run(ctx, "switch", "--quiet", "--detach", ev.Base); err != nil {
			return err
		}
		if err := deleteBranch(ctx, g, r.Branch, ev); err != nil {
			return err
		}
		r.Branch, r.BaseSHA = "", ev.Base
		repos = append(repos, r)
	}
	return m.Store.Write(ctx, func(tx *sql.Tx) error {
		l := d.lane
		l.Name, l.Purpose, l.State, l.Pooled, l.Updated = "", "", state.LanePool, state.Now(), state.Now()
		if err := state.UpdateLane(tx, l); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET lane = NULL, lane_generation = NULL WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		return state.SetLaneRepos(tx, l.Number, repos)
	})
}

// deleteBranch deletes a proven-merged lane branch: with -d when Git itself sees it merged, and
// with -D only when it still points at the head that was proven.
func deleteBranch(ctx context.Context, g run.Git, branch string, ev evidence.Repo) error {
	if branch == "" {
		return nil
	}
	head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return nil // already gone
	}
	if head != ev.Head {
		return fmt.Errorf("branch %s moved to %s after it was proven merged at %s; it was kept", branch, head[:12], ev.Head[:12])
	}
	flag := "-d"
	if ev.Proof != evidence.Ancestor {
		flag = "-D"
	}
	_, err = g.Run(ctx, "branch", flag, branch)
	return err
}

// destroy removes a lane: its project worktrees, then the lane root, then its record. Nothing is
// forced: a worktree Git will not remove keeps the lane, and the journal says so.
func (m *Manager) destroy(ctx context.Context, l state.Lane, stored []state.LaneRepo, archived bool) error {
	if stored == nil {
		var err error
		if stored, err = state.LaneRepos(m.Store.DB(), l.Number); err != nil {
			return err
		}
	}
	lifecycle.Stop(ctx, m.Store, l.Number)
	if err := m.teardown(ctx, l); err != nil {
		return err
	}
	if err := m.Data().DropLane(ctx, m.ProviderLane(l)); err != nil {
		return err
	}
	slices.SortFunc(stored, func(a, b state.LaneRepo) int { return strings.Compare(b.Project, a.Project) }) // the root ("") last
	for _, r := range stored {
		owner := filepath.Join(m.WS.Owner, m.relOf(r))
		g := run.In(owner)
		if _, err := os.Stat(r.Path); err == nil {
			if r.Project == "" {
				m.removeRootLinks(r.Path)
			}
			removeMaterialized(r.Path)
			if _, err := worktreeGit(ctx, g, "", "worktree", "remove", r.Path); err != nil {
				return err
			}
		} else if _, err := worktreeGit(ctx, g, "", "worktree", "prune"); err != nil {
			return err
		}
		if r.Branch != "" {
			if head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch); err == nil {
				flag := "-d"
				if archived {
					flag = "-D" // its head is under refs/aw/archive
				} else if ok, _ := g.OK(ctx, "merge-base", "--is-ancestor", head, r.BaseRef); !ok {
					flag = "-D" // proven by patch equivalence or the forge
				}
				if _, err := g.Run(ctx, "branch", flag, r.Branch); err != nil {
					return err
				}
			}
		}
	}
	if root := m.Root(l.Number); workspace.IsGitRepo(root) {
		os.Remove(root)
	} else {
		os.RemoveAll(root) // a lane root that is not a worktree: links and copies
	}
	if err := m.Store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE sessions SET lane = NULL, lane_generation = NULL WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM lane_data WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		if err := state.ForgetSetup(tx, l.Number); err != nil {
			return err
		}
		return state.DeleteLane(tx, l.Number)
	}); err != nil {
		return err
	}
	// The next lane with this number starts empty: no env files, and no stamps of installs it has not had.
	return os.RemoveAll(filepath.Join(filepath.Dir(m.Store.Path()), "lanes", Dir(l.Number, m.Cfg.Lanes.Max)))
}

// Data is the workspace's providers.
func (m *Manager) Data() *providers.Set {
	return &providers.Set{Cfg: m.Cfg, Store: m.Store, Owner: m.WS.Owner, StateDir: m.StateDir}
}

// ProviderLane is what a provider is told about a lane.
func (m *Manager) ProviderLane(l state.Lane) providers.Lane {
	root := m.Root(l.Number)
	dir, _ := m.StateDir(l.Number)
	return providers.Lane{Number: l.Number, Dir: Dir(l.Number, m.Cfg.Lanes.Max), Name: l.Name, Root: root, State: dir}
}

// teardown runs the workspace's teardown hook. It must be safe to rerun: a failure keeps the lane,
// and aw sweep --apply --retry runs it again.
func (m *Manager) teardown(ctx context.Context, l state.Lane) error {
	h := m.Cfg.Lifecycle.Teardown
	if h == nil {
		return nil
	}
	if _, err := os.Stat(m.Root(l.Number)); err != nil {
		return nil // already removed by an earlier attempt, which got past this
	}
	release := func() {}
	if len(h.Needs) > 0 {
		var err error
		if release, err = m.Seats(ctx, l, h.Needs); err != nil {
			return fmt.Errorf("teardown of lane %s: %w", l.Name, err)
		}
	}
	defer release()
	where, err := m.LocateIn(l.Number, "")
	if err != nil {
		return err
	}
	dir, err := m.StateDir(l.Number)
	if err != nil {
		return err
	}
	log := filepath.Join(dir, "logs", "setup", "teardown.log")
	if _, err := lifecycle.Exec(ctx, lifecycle.Command{Dir: where.Dir, Env: where.Env, Run: h.Run, Timeout: time.Duration(h.Timeout), Log: log, Phase: lifecycle.PhaseTeardown}); err != nil {
		return fmt.Errorf("teardown of lane %s failed: %v (log %s)", l.Name, err, log)
	}
	return nil
}

// removeRootLinks removes what aw put in a lane root (project links and the owner pointer), which
// are ignored files Git would otherwise leave behind.
func (m *Manager) removeRootLinks(root string) {
	for _, name := range sortedKeys(m.Cfg.Projects) {
		p := m.Cfg.Projects[name]
		if p.Directory == "." {
			continue
		}
		link := filepath.Join(root, p.Directory)
		if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
			os.Remove(link)
		}
	}
	os.Remove(filepath.Join(root, ".aw", "owner"))
	os.Remove(filepath.Join(root, ".aw"))
	sandbox.Remove(root)
}

// CallerLane is the number of the lane a command runs in, or 0 in the owner checkout.
func (m *Manager) CallerLane() int {
	if m.WS.LaneRoot == "" {
		return 0
	}
	n, err := strconv.Atoi(filepath.Base(m.WS.LaneRoot))
	if err != nil || !samePlace(m.Root(n), m.WS.LaneRoot) {
		return 0
	}
	return n
}

// samePlace compares paths through symlinks: a process without $PWD sees the physical path, and the
// owner checkout may be recorded through a link.
func samePlace(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// Resolve finds a lane by name, number (3 or 03) or unique name prefix. Names are checked first.
func (m *Manager) Resolve(ref string) (state.Lane, error) {
	lanes, err := state.Lanes(m.Store.DB())
	if err != nil {
		return state.Lane{}, err
	}
	if n, err := strconv.Atoi(ref); err == nil {
		for _, l := range lanes {
			if l.Number == n {
				return l, nil
			}
		}
		return state.Lane{}, fmt.Errorf("there is no lane %s → run aw to list the lanes", ref)
	}
	if err := CheckName(ref); err != nil {
		return state.Lane{}, err
	}
	var prefix []state.Lane
	for _, l := range lanes {
		if l.Name == ref {
			return l, nil
		}
		if strings.HasPrefix(l.Name, ref) {
			prefix = append(prefix, l)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], nil
	case 0:
		return state.Lane{}, fmt.Errorf("there is no lane named %q → aw lanes lists them", ref)
	}
	var names []string
	for _, l := range prefix {
		names = append(names, l.Name)
	}
	return state.Lane{}, fmt.Errorf("%q matches several lanes (%s) → give more of the name", ref, strings.Join(names, ", "))
}

// ErrDirty is returned when an operation would lose uncommitted work.
var ErrDirty = errors.New("uncommitted work")
