package lanes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// ActionReclaim: removed only with --pushed. The lane's work is not merged, but the forge holds
	// it, in an open change at exactly the lane's head, so its branches are archived and it is removed.
	ActionReclaim = "reclaim"
)

// What keeps a lane (Decision.Kept), so that a list of kept lanes can be read at a glance (#126).
const (
	KeptHeld      = "held"      // a session holds it, it is parked or being set up, or it has open notes
	KeptUnmerged  = "unmerged"  // a repository has uncommitted work or work not proven merged
	KeptStuck     = "stuck"     // an earlier cleanup of it failed part way
	KeptProtected = "protected" // something about it cannot be read, so it is kept to be safe
)

// Holder is a session that holds a lane: what sweep names so that a person can find and end it.
type Holder struct {
	Session string `json:"session"`
	Harness string `json:"harness"`
	PID     int    `json:"pid,omitempty"`
	TTY     string `json:"tty,omitempty"`
	// Since is when the session started.
	Since string `json:"since,omitempty"`
	// Idle, for a shell that aw started in the lane, says why it holds the lane for nothing: its
	// working directory is no longer in the lane (#128).
	Idle string `json:"idle,omitempty"`
}

// Decision is what sweep does with one lane, and why.
type Decision struct {
	Number int             `json:"number"`
	Dir    string          `json:"lane"`
	Name   string          `json:"name"`
	State  string          `json:"state"`
	Action string          `json:"action"`
	Reason string          `json:"reason"`
	Repos  []evidence.Repo `json:"repos,omitempty"`
	// Kept, for a kept lane, is what kind of thing keeps it: KeptHeld, KeptUnmerged, KeptStuck or
	// KeptProtected. Action stays "keep", so scripts that read it are unchanged.
	Kept string `json:"kept,omitempty"`
	// Holders are the sessions that hold the lane, when sessions are what keeps it.
	Holders []Holder         `json:"holders,omitempty"`
	Next    []string         `json:"next,omitempty"`   // for a kept lane, what would let it be recycled
	Result  string           `json:"result,omitempty"` // after --apply: done, skipped, failed
	Error   string           `json:"error,omitempty"`
	lane    state.Lane       `json:"-"`
	stored  []state.LaneRepo `json:"-"`
	// partial: an earlier removal that did not finish already took some of its worktrees, so it can
	// only be removed, not recycled.
	partial bool
}

// SweepReport is the result of aw sweep.
type SweepReport struct {
	Applied bool                 `json:"applied"`
	Lanes   []*Decision          `json:"lanes"`
	Open    []state.JournalEntry `json:"unfinished"` // cleanups that failed earlier
	// UnrecognizedForges are the origins' hosts that forge = "auto" did not recognize, so that a
	// squash-merged lane there could not be proven merged.
	UnrecognizedForges []string `json:"unrecognized_forges,omitempty"`
	// Stale are the unfinished entries whose lane no longer exists, by id: nothing is left for a
	// retry to finish, so --apply closes them.
	Stale []int `json:"stale,omitempty"`
	// Closed are the stale entries this --apply closed, by id.
	Closed []int `json:"closed,omitempty"`
	// PrunedArchives are the archived tips --apply removed for being older than
	// [lanes] archive_retention, as "<repository> <ref>".
	PrunedArchives []string `json:"pruned_archives,omitempty"`
}

// SweepOptions configure a sweep.
type SweepOptions struct {
	Apply  bool
	Caller int // the lane the command runs in, which is never swept
	Retry  int // a journal entry to retry
	// Only, when set, are the lanes to report and act on; the rest are left alone.
	Only []int
	Now  time.Time
	// Pushed also removes the lanes whose unmerged work is in an open change on the forge at exactly
	// their heads (ActionReclaim). Without it, those lanes are reported and left alone.
	Pushed bool
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
	// An entry whose lane is gone, with no worktree left in any repository, has nothing left to
	// finish: --apply closes it, a retry included, rather than reporting it forever.
	exists := map[int]bool{}
	for _, l := range lanes {
		exists[l.Number] = true
	}
	for _, e := range open {
		if e.Lane == 0 || exists[e.Lane] || m.hasWorktrees(e.Lane) {
			continue
		}
		rep.Stale = append(rep.Stale, e.ID)
		if !o.Apply {
			continue
		}
		why := "closed by aw sweep --apply: the lane no longer exists and no repository has a worktree for it"
		if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.EndJournal(tx, e.ID, state.JournalDone, e.Detail+" ("+why+")") }); err != nil {
			return nil, err
		}
		rep.Closed = append(rep.Closed, e.ID)
	}
	if o.Apply && len(rep.Closed) > 0 {
		open = slices.DeleteFunc(open, func(e state.JournalEntry) bool { return slices.Contains(rep.Closed, e.ID) })
		rep.Open = slices.DeleteFunc(rep.Open, func(e state.JournalEntry) bool { return slices.Contains(rep.Closed, e.ID) })
		if slices.Contains(rep.Closed, o.Retry) {
			rep.Lanes = []*Decision{}
			return rep, nil // the retried entry was stale: closing it is the whole retry
		}
	}
	failedLanes := map[int]state.JournalEntry{}
	retryLane := 0
	for _, e := range open {
		failedLanes[e.Lane] = e
		if e.ID == o.Retry {
			retryLane = e.Lane
		}
	}
	if o.Retry > 0 && retryLane == 0 {
		return nil, fmt.Errorf("no unfinished cleanup has the number %d → aw sweep lists the unfinished ones", o.Retry)
	}

	m.forges, m.forgeDeadline, m.unrecognized = map[string]*forge.Client{}, o.Now.Add(30*time.Second), nil
	pooled := 0
	for _, l := range lanes {
		d := &Decision{Number: l.Number, Dir: Dir(l.Number, m.Cfg.Lanes.Max), Name: l.Name, State: l.State, lane: l}
		rep.Lanes = append(rep.Lanes, d)
		if l.State == state.LanePool {
			continue // decided below, once the recyclable lanes are known
		}
		// Checking a lane is usually quick; one with large repositories says it is still at it (#125).
		stop := Beat(m.Progress, fmt.Sprintf("%s %s: still checking its repositories", d.Dir, laneName(l)))
		m.decide(ctx, d, sessions, o.Caller)
		stop()
		if l.State == state.LaneClaimed {
			d.State = QuickState(m.Store.DB(), l, sessions)
			if d.Action == ActionRecycle {
				d.State = Recyclable
			}
		}
		// The proof is still worked out, so that whoever fixes the failure can see whether the lane
		// is safe to recycle.
		if e, ok := failedLanes[l.Number]; ok && o.Retry != e.ID {
			d.Action, d.Reason = ActionKeep, fmt.Sprintf("an earlier %s failed (%s) → fix the cause, then aw sweep --apply%s --retry %d", e.Kind, e.Detail, pushedFlag(e.Kind), e.ID)
			d.Kept, d.Holders, d.Next = KeptStuck, nil, nil
		}
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
			d.Action, d.Reason, d.Kept = ActionKeep, "its age in the pool is unknown, so it is kept", KeptProtected
		case retention > 0 && o.Now.Sub(pooledAt) > retention:
			d.Action, d.Reason = ActionDestroy, fmt.Sprintf("in the pool for more than %s ([lanes] retention)", retention)
		case pooled >= m.Cfg.Lanes.Pool:
			d.Action, d.Reason = ActionDestroy, fmt.Sprintf("the pool already has %d lanes ([lanes] pool)", m.Cfg.Lanes.Pool)
		default:
			pooled++
			d.Action, d.Reason = ActionPooled, fmt.Sprintf("recycled: its branches and work are removed; kept on disk as a warm lane, ready to be claimed (pool %d of %d)", pooled, m.Cfg.Lanes.Pool)
		}
	}
	for _, d := range rep.Lanes {
		if d.Action == ActionRecycle && d.partial {
			d.Action, d.Reason = ActionDestroy, d.Reason+"; an earlier removal already took some of its worktrees, so its removal is finished"
		} else if d.Action == ActionRecycle && pooled >= m.Cfg.Lanes.Pool {
			d.Action, d.Reason = ActionDestroy, d.Reason+"; the pool is full ([lanes] pool), so it is removed"
		} else if d.Action == ActionRecycle {
			pooled++
		}
	}

	// The pool is worked out from every lane, then the report narrowed to the lanes asked for.
	if len(o.Only) > 0 {
		rep.Lanes = slices.DeleteFunc(rep.Lanes, func(d *Decision) bool { return !slices.Contains(o.Only, d.Number) })
	}
	rep.UnrecognizedForges = m.unrecognized
	if !o.Apply {
		return rep, nil
	}
	for _, d := range rep.Lanes {
		if d.Action != ActionRecycle && d.Action != ActionDestroy && (d.Action != ActionReclaim || !o.Pushed) {
			continue
		}
		if retryLane != 0 && d.Number != retryLane {
			d.Result = "skipped" // --retry acts on its own lane only
			continue
		}
		if err := m.applyTelling(ctx, d, o); err != nil {
			d.Result, d.Error = "failed", err.Error()
		}
	}
	if len(o.Only) == 0 && o.Retry == 0 {
		rep.PrunedArchives = m.pruneArchives(ctx, o.Now)
	}
	rep.Open, _ = state.OpenJournal(m.Store.DB())
	if rep.Open == nil {
		rep.Open = []state.JournalEntry{}
	}
	return rep, nil
}

// hasWorktrees reports whether lane n's root, or any project's worktree for it, is still on disk.
func (m *Manager) hasWorktrees(n int) bool {
	dir := Dir(n, m.Cfg.Lanes.Max)
	paths := []string{m.Root(n)}
	for _, p := range m.Cfg.Projects {
		if p.Directory != "." {
			paths = append(paths, filepath.Join(m.WS.Owner, p.Directory, ".worktrees", dir))
		}
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// decide examines a lane that is not in the pool. Anything that cannot be read keeps the lane.
func firstNote(notes []state.Note) string {
	if len(notes) == 0 {
		return ""
	}
	return notes[0].Text
}

func (m *Manager) decide(ctx context.Context, d *Decision, sessions []state.Session, caller int) {
	// A reason to keep the lane that is not about its repositories still leaves their proof to be
	// worked out and reported, without asking the forge: it would not change the decision.
	var held string
	var holders []state.Session
	keep := func(kind, format string, args ...any) {
		d.Action, d.Reason, d.Kept = ActionKeep, fmt.Sprintf(format, args...), kind
		if held != "" {
			d.Reason, d.Next, d.Kept = held, nil, KeptHeld
			if len(holders) > 0 {
				d.Holders = describeHolders(holders, d.lanePaths(m))
				d.Next = endSteps(d.Holders)
				d.Reason = attachedLine(d.Holders)
			}
		}
	}
	l := d.lane
	switch l.State {
	case state.LaneClaiming, state.LaneSettingUp:
		keep(KeptHeld, "it is being set up")
		return // its worktrees may not exist yet
	case state.LaneIncomplete:
		held = "its setup is incomplete"
	case state.LaneParked:
		held = "parked"
	}
	if held == "" && l.Number == caller {
		held = "this command runs in it"
	}
	if held == "" {
		for _, s := range sessions {
			if s.Holds(l) {
				holders = append(holders, s)
			}
		}
		if len(holders) > 0 {
			held = "held" // worded by keep, once the holders are described
		}
	}
	// An open note is work the lane's agent left outside it, which no merge proves done.
	if notes, err := state.OpenNotes(m.Store.DB(), l.Number, l.Generation); held == "" && (err != nil || len(notes) > 0) {
		held = fmt.Sprintf("it has %d open note%s, such as %q → do them, or mark them done with aw note --done <n> in the lane", len(notes), plural(len(notes)), firstNote(notes))
	}
	root := m.Root(l.Number)
	if info, err := os.Lstat(root); err != nil {
		keep(KeptProtected, "its directory %s cannot be read: %v", root, err)
		return
	} else if info.Mode()&os.ModeSymlink != 0 {
		keep(KeptProtected, "its directory %s is a symlink, not the lane aw made", root)
		return
	}
	if data, err := os.ReadFile(filepath.Join(root, ".aw", "owner")); err != nil || strings.TrimSpace(string(data)) != m.WS.Owner {
		keep(KeptProtected, "its lane marker (.aw/owner) does not name this owner checkout")
		return
	}
	stored, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil || len(stored) == 0 {
		keep(KeptProtected, "its repositories cannot be read from the state")
		return
	}
	d.stored = stored
	var reasons []string
	stale := false // a repository's proof is from a fetch older than freshFetch
	for _, r := range stored {
		owner := filepath.Join(m.WS.Owner, m.relOf(r))
		if locked, err := worktreeLocked(ctx, owner, r.Path); err != nil || locked {
			keep(KeptProtected, "%s's worktree is locked (git worktree lock), or its lock cannot be read", repoLabel(r))
			return
		}
		var ev evidence.Repo
		if _, err := os.Lstat(r.Path); errors.Is(err, fs.ErrNotExist) {
			// Already removed by an earlier removal that did not finish: what is left is its branch.
			ev = evidence.CheckRemoved(ctx, owner, r.Path, r.Branch, r.BaseRef)
			d.partial = true
		} else {
			ev = evidence.Check(ctx, r.Path, r.BaseRef, m.evidenceOptions(r))
		}
		if held == "" && !ev.Unknown && ev.Clean && ev.Proof == "" {
			m.forgeProof(ctx, owner, r, &ev)
		}
		d.Repos = append(d.Repos, ev)
		switch {
		case ev.Unknown && held != "":
			continue // kept anyway; the other repositories' proof is still worth showing
		case ev.Unknown:
			keep(KeptProtected, "%s: %s, so it is protected", repoLabel(r), ev.Reason)
			return
		case !ev.Clean:
			reasons = append(reasons, repoLabel(r)+": "+ev.Dirty)
			d.Next = append(d.Next, cleanStep(r, ev))
		case ev.Proof == "":
			if note := m.fetchNote(owner, r.BaseRef, &ev); note != "" {
				ev.Reason += note
				stale = true
			}
			d.Repos[len(d.Repos)-1] = ev
			reasons = append(reasons, repoLabel(r)+": "+ev.Reason)
			d.Next = append(d.Next, fmt.Sprintf("%s: merge its work (aw changeset opens the merge requests), or aw resume %s to keep working on it", repoLabel(r), l.Name))
		}
	}
	if stale {
		// The proof is only as fresh as the last fetch: work merged since is not seen until then.
		d.Next = append([]string{"aw pull fetches origin first: work merged since the last fetch is then proven merged"}, d.Next...)
	}
	if held != "" {
		keep(KeptHeld, "%s", held)
		if len(holders) > 0 && allProven(d.Repos, stored) {
			// Only the sessions stand in the way: say so, and when it will go (#126).
			them := "that session ends"
			if len(holders) > 1 {
				them = "they end"
			}
			d.Reason += "; every repository is clean and proven merged, so it will be recycled once " + them
		}
		return
	}
	if pushed, ok := inReview(d.Repos, d.stored); ok && len(reasons) > 0 {
		d.Action, d.Reason = ActionReclaim, "pushed and in review: "+strings.Join(pushed, "; ")
		d.Next = []string{fmt.Sprintf("aw sweep --pushed --apply %s removes it: its branches are archived under %s%s first, and its lane data is dropped", l.Name, ArchivePrefix, l.Name)}
		return
	}
	if len(reasons) > 0 {
		prefix := ""
		if state.HandedOff(m.Store.DB(), l.Number, l.Generation) {
			prefix = "ready for review, but "
		}
		keep(KeptUnmerged, "%s%s", prefix, strings.Join(reasons, "; "))
		return
	}
	d.Action, d.Reason = ActionRecycle, "every repository is clean and proven merged"
}

// freshFetch is how old origin's refs in a repository may be before sweep says that a lane it
// keeps as not proven merged may be merged already (#127). aw never fetches on a read.
const freshFetch = time.Hour

// fetchNote says, for a repository not proven merged against a remote-tracking base, when that base
// was last fetched, when that is longer ago than freshFetch, and records it in ev. It is "" when the
// base is local, the fetch is recent, or the time cannot be read.
func (m *Manager) fetchNote(owner, baseRef string, ev *evidence.Repo) string {
	if !strings.HasPrefix(baseRef, "refs/remotes/") {
		return ""
	}
	at := fetchedAt(owner, baseRef)
	if at.IsZero() {
		return ""
	}
	ev.LastFetch = at.UTC().Format(time.RFC3339)
	since := time.Since(at)
	if since <= freshFetch {
		return ""
	}
	return fmt.Sprintf("; %s was last fetched %s ago, so a merge since then is not seen → aw pull", strings.TrimPrefix(baseRef, "refs/remotes/"), age(since))
}

// allProven reports whether every repository of a lane is clean and proven merged.
func allProven(repos []evidence.Repo, stored []state.LaneRepo) bool {
	if len(repos) != len(stored) {
		return false
	}
	for _, ev := range repos {
		if ev.Unknown || !ev.Clean || ev.Proof == "" {
			return false
		}
	}
	return true
}

// attachedLine names every session that holds a lane, and which of them are idle shells.
func attachedLine(holders []Holder) string {
	names := make([]string, len(holders))
	for i, h := range holders {
		idle := ""
		if h.Idle != "" {
			idle = ", idle"
		}
		names[i] = fmt.Sprintf("%s (%s%s)", h.Session, h.Harness, idle)
	}
	if len(holders) == 1 {
		return "session " + names[0] + " is attached"
	}
	return fmt.Sprintf("%d sessions are attached: %s", len(holders), strings.Join(names, ", "))
}

// lanePaths are the directories that are inside the lane: its root and each repository's worktree.
func (d *Decision) lanePaths(m *Manager) []string {
	paths := []string{m.Root(d.lane.Number)}
	for _, r := range d.stored {
		paths = append(paths, r.Path)
	}
	return paths
}

// describeHolders gives each holding session's process and terminal, for finding it, and says when
// a shell holds the lane though its directory has left it.
func describeHolders(holders []state.Session, lanePaths []string) []Holder {
	out := make([]Holder, len(holders))
	for i, h := range holders {
		out[i] = Holder{Session: h.HarnessSession, Harness: h.Harness, PID: h.PID}
		if t, err := time.Parse(time.RFC3339Nano, h.Started); err == nil {
			out[i].Since = t.UTC().Format(time.RFC3339)
		}
		if h.PID <= 0 {
			continue
		}
		out[i].TTY = ttyOf(h.PID)
		if h.Harness == "shell" {
			if cwd := cwdOf(h.PID); cwd != "" && !inside(cwd, lanePaths) {
				out[i].Idle = "its directory is no longer in the lane (it is in " + cwd + ")"
			}
		}
	}
	return out
}

// inside reports whether dir is one of paths or below one of them.
func inside(dir string, paths []string) bool {
	for _, p := range paths {
		if rel, err := filepath.Rel(realPath(p), realPath(dir)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// endSteps say, for each holding session, how to end it or let the lane go.
func endSteps(holders []Holder) []string {
	var steps []string
	for _, h := range holders {
		where := ""
		switch {
		case h.PID > 0 && h.TTY != "":
			where = fmt.Sprintf(", pid %d on %s", h.PID, h.TTY)
		case h.PID > 0:
			where = fmt.Sprintf(", pid %d", h.PID)
		}
		how := "end it, or run aw detach in it"
		switch {
		case h.Idle != "":
			since := ""
			if t, err := time.Parse(time.RFC3339, h.Since); err == nil {
				since = " since " + t.Local().Format("Jan 2")
			}
			how = fmt.Sprintf("an idle shell that has held the lane%s, but %s → exit that shell to release it", since, h.Idle)
		case h.Harness == "shell":
			how = "exit that shell, or run aw detach in it"
		}
		steps = append(steps, fmt.Sprintf("session %s (%s%s): %s", h.Session, h.Harness, where, how))
	}
	return steps
}

// cwdOf is a process's working directory, or "" when it cannot be read.
func cwdOf(pid int) string {
	if runtime.GOOS == "linux" {
		dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if err != nil {
			return ""
		}
		return dir
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if dir, ok := strings.CutPrefix(line, "n"); ok && filepath.IsAbs(dir) {
			return dir
		}
	}
	return ""
}

// ttyOf is the terminal a process runs on, such as ttys003 or pts/2, or "" when it has none or it
// cannot be read. ps reports it the same way on Linux and macOS.
func ttyOf(pid int) string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || tty == "?" || tty == "??" || strings.ContainsFunc(tty, func(r rune) bool { return r < ' ' }) {
		return ""
	}
	return tty
}

// inReview reports whether a lane whose repositories are all clean can be reclaimed: each one is
// proven merged, or the forge reports an open change whose head is exactly the repository's head,
// and at least one is in such a change. It says, for each change, where the work is.
func inReview(repos []evidence.Repo, stored []state.LaneRepo) ([]string, bool) {
	if len(repos) != len(stored) {
		return nil, false
	}
	var pushed []string
	for i, ev := range repos {
		switch {
		case ev.Unknown || !ev.Clean:
			return nil, false
		case ev.Proof != "":
			continue
		}
		ch, _ := ev.Forge.(*forge.Change)
		if ch == nil || ch.Availability != forge.Verified || ch.State != "opened" || ch.SourceHead != ev.Head || ev.Head == "" {
			return nil, false
		}
		draft := ""
		if ch.Draft {
			draft = "draft "
		}
		pushed = append(pushed, fmt.Sprintf("%s: %schange !%d is open at %s", repoLabel(stored[i]), draft, ch.IID, short(ev.Head)))
	}
	return pushed, len(pushed) > 0
}

// pushedFlag is the flag a sweep needs to carry out an action again.
func pushedFlag(action string) string {
	if action == ActionReclaim {
		return " --pushed"
	}
	return ""
}

func short(sha string) string { return sha[:min(len(sha), 12)] }

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
	// Whatever stops the forge being asked is said, so that "not proven" names every check tried.
	raw, err := run.In(owner).Line(ctx, "config", "--get", "remote."+remote+".url")
	if err != nil {
		ev.Reason += fmt.Sprintf("; no forge was asked, since %s has no URL", remote)
		return
	}
	host, project, ok := forge.ParseRemote(raw)
	if !ok {
		ev.Reason += fmt.Sprintf("; no forge was asked, since %s is not on one", remote)
		return
	}
	c, seen := m.forges[host]
	if !seen {
		c = forge.For(m.Cfg.Workspace.Forge, host, m.forgeDeadline)
		m.forges[host] = c
	}
	if c == nil {
		if m.Cfg.Workspace.Forge == "none" {
			ev.Reason += `; no forge was asked (forge = "none")`
			return
		}
		ev.Reason += fmt.Sprintf(`; no forge was asked, since forge = "auto" does not recognize %s`, host)
		if !slices.Contains(m.unrecognized, host) {
			m.unrecognized = append(m.unrecognized, host)
		}
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
	if r.Branch == "" {
		o.DetachedAt = r.BaseSHA // reset by a recycle that did not finish
	}
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

// applyTelling is apply, telling Progress which lane it works on and each step it takes, so that a
// long --apply never looks like a hang (#125).
func (m *Manager) applyTelling(ctx context.Context, d *Decision, o SweepOptions) error {
	prev := m.Progress
	if prev == nil {
		return m.apply(ctx, d, o)
	}
	label := fmt.Sprintf("%s %s", d.Dir, laneName(d.lane))
	m.Progress = func(line string) { prev(label + ": " + line) }
	defer func() { m.Progress = prev }()
	verb := map[string]string{ActionRecycle: "recycling it", ActionDestroy: "removing it", ActionReclaim: "archiving its branches and removing it"}[d.Action]
	if verb != "" {
		m.say("%s", verb)
	}
	return m.apply(ctx, d, o)
}

// laneName is a lane's name for progress lines, or "(pool)" for a warm lane.
func laneName(l state.Lane) string {
	if l.Name == "" {
		return "(pool)"
	}
	return l.Name
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
		want := ActionRecycle // a lane removed for a full pool was decided recyclable
		if d.Action == ActionReclaim {
			want = ActionReclaim
		}
		if recheck.Action != want || recheck.fingerprint() != before {
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
		err = m.destroy(ctx, d.lane, d.stored, false, false)
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
		err = fmt.Errorf("%w → fix the cause, then aw sweep --apply%s --retry %d", err, pushedFlag(d.Action), jid)
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
	// Each repository is recorded as it is reset, so that a recycle that fails part way leaves a
	// record of what it did, and a retry continues from there instead of finding a detached HEAD.
	repos := slices.Clone(d.stored)
	var done []string
	for i, r := range repos {
		ev := d.Repos[i]
		// The post-checkout hook is a setup step of the next lane, which honours post_checkout.
		g := run.In(r.Path, noHooks...)
		failed := func(err error) error {
			if len(done) == 0 {
				return fmt.Errorf("%s: %w", repoLabel(r), err)
			}
			return fmt.Errorf("%s: %w (already reset: %s; a retry continues from here)", repoLabel(r), err, strings.Join(done, ", "))
		}
		// Each repository says it is being reset, and a slow one that it still is (#125).
		m.say("%s: resetting it to its base", repoLabel(r))
		stop := Beat(m.Progress, repoLabel(r)+": still resetting it to its base")
		err := func() error {
			removeMaterialized(r.Path) // a copy that replaced a tracked file must not travel to the base
			if _, err := g.Run(ctx, "switch", "--quiet", "--detach", ev.Base); err != nil {
				return err
			}
			// Proven by anything but ancestry, the commits themselves are not in the base, though
			// their changes are: the tip is archived first, so that its history is not lost either.
			if r.Branch != "" && ev.Proof != evidence.Ancestor {
				if err := archiveTip(ctx, g, d.lane.Name, ev.Head, time.Now()); err != nil {
					return err
				}
			}
			return deleteBranch(ctx, g, r.Branch, ev)
		}()
		stop()
		if err != nil {
			return failed(err)
		}
		repos[i].Branch, repos[i].BaseSHA = "", ev.Base
		if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetLaneRepos(tx, d.Number, repos) }); err != nil {
			return failed(err)
		}
		done = append(done, repoLabel(r))
	}
	return m.Store.Write(ctx, func(tx *sql.Tx) error {
		l := d.lane
		l.Name, l.Purpose, l.State, l.Pooled, l.Updated = "", "", state.LanePool, state.Now(), state.Now()
		if err := state.UpdateLane(tx, l); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET lane = NULL WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		return state.SetLaneRepos(tx, l.Number, repos)
	})
}

// deleteBranch deletes a proven-merged lane branch, only while it still points at the head that
// was proven.
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
	return dropBranch(ctx, g, branch, head)
}

// dropBranch deletes a lane branch that aw has proven merged (or archived) at head. aw's proof is
// against the lane's base, so Git's own check is not asked: git branch -d judges "merged" against
// the current HEAD or the branch's upstream, and the owner checkout may be on any branch (#112).
// The ref is deleted only if it still points at head, so commits made since are never lost, and
// the branch's configuration (its upstream) goes with it, as git branch -D would.
func dropBranch(ctx context.Context, g run.Git, branch, head string) error {
	if _, err := g.Run(ctx, "update-ref", "-d", "refs/heads/"+branch, head); err != nil {
		return err
	}
	_, _ = g.Run(ctx, "config", "--remove-section", "branch."+branch) // fails only when there is none
	return nil
}

// destroy removes a lane: its project worktrees, then the lane root, then its record. Nothing is
// forced unless force says the human chose to lose uncommitted work (aw discard --force): otherwise
// a worktree Git will not remove keeps the lane, and the journal says so.
func (m *Manager) destroy(ctx context.Context, l state.Lane, stored []state.LaneRepo, archived, force bool) error {
	if stored == nil {
		var err error
		if stored, err = state.LaneRepos(m.Store.DB(), l.Number); err != nil {
			return err
		}
	}
	// Each step says what it does, and a slow one says it is still at it: removing a large lane
	// can take minutes, and silence looks like a hang.
	step := func(what string, fn func() error) error {
		m.say("%s", what)
		stop := Beat(m.Progress, "still "+what)
		defer stop()
		return fn()
	}
	if err := step("stopping its servers", func() error { lifecycle.Stop(ctx, m.Store, l.Number); return nil }); err != nil {
		return err
	}
	if err := step("running its teardown", func() error { return m.teardown(ctx, l) }); err != nil {
		return err
	}
	if err := step("dropping its data", func() error { return m.Data().DropLane(ctx, m.ProviderLane(l)) }); err != nil {
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
			if err := step(repoLabel(r)+": removing its worktree", func() error {
				args := []string{"worktree", "remove", r.Path}
				if force {
					args = []string{"worktree", "remove", "--force", r.Path} // aw discard --force: the human chose to lose uncommitted work
				}
				_, err := worktreeGit(ctx, g, "", args...)
				return err
			}); err != nil {
				return err
			}
		} else if _, err := worktreeGit(ctx, g, "", "worktree", "prune"); err != nil {
			return err
		}
		if r.Branch != "" {
			tip, _ := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch)
			if err := releaseBackups(ctx, g, l.Name, r.BaseRef, tip, time.Now()); err != nil {
				return err
			}
			if head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch); err == nil {
				// Archived, its head is under refs/aw/archive. Otherwise it was proven merged into
				// its base: by ancestry, or by patch, content or the forge, which archive it first, as
				// a recycle does.
				if !archived {
					if ok, _ := g.OK(ctx, "merge-base", "--is-ancestor", head, r.BaseRef); !ok {
						if err := archiveTip(ctx, g, l.Name, head, time.Now()); err != nil {
							return err
						}
					}
				}
				if err := dropBranch(ctx, g, r.Branch, head); err != nil {
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
		if _, err := tx.Exec(`UPDATE sessions SET lane = NULL WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM lane_data WHERE lane = ?`, l.Number); err != nil {
			return err
		}
		if err := state.ForgetSetup(tx, l.Number); err != nil {
			return err
		}
		// What failed on this lane earlier is over with it: the lane is gone.
		if err := state.CloseLaneJournal(tx, l.Number, "the lane was removed"); err != nil {
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

// archiveTip keeps head under refs/aw/archive/<name>/<time>, as aw discard does.
func archiveTip(ctx context.Context, g run.Git, name, head string, now time.Time) error {
	stamp := now.UTC().Format(archiveStamp)
	ref := ArchivePrefix + name + "/" + stamp
	for n := 2; ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", ref)); n++ {
		ref = fmt.Sprintf("%s%s/%s-%d", ArchivePrefix, name, stamp, n)
	}
	_, err := g.Run(ctx, "update-ref", ref, head, "")
	return err
}

// archiveStamp is how an archive's ref names its time.
const archiveStamp = "20060102-150405"

// pruneArchives removes archived tips older than [lanes] archive_retention from every repository,
// and returns what it removed. An archive whose time cannot be read from its name is kept.
func (m *Manager) pruneArchives(ctx context.Context, now time.Time) []string {
	keep := time.Duration(m.Cfg.Lanes.ArchiveRetention)
	if keep <= 0 {
		return nil
	}
	all, err := m.Archives(ctx)
	if err != nil {
		return nil
	}
	dirs := map[string]string{}
	for _, r := range m.archiveRepos() {
		dirs[r[0]] = r[1]
	}
	var removed []string
	for _, a := range all {
		// A second archive of one name within a second ends in -<n>, after the time.
		at, err := time.Parse(archiveStamp, a.Time[:min(len(a.Time), len(archiveStamp))])
		if err != nil || now.Sub(at) <= keep {
			continue
		}
		for _, r := range a.Repos {
			if _, err := run.In(dirs[r.Repo]).Run(ctx, "update-ref", "-d", r.Ref, r.Head); err == nil {
				removed = append(removed, r.Repo+" "+r.Ref)
			}
		}
	}
	return removed
}
