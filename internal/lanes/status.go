package lanes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/connor981332/agent-workspace/internal/evidence"
	"github.com/connor981332/agent-workspace/internal/forge"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Dashboard sections (ux.md, "After a reboot").
const (
	NeedsYou  = "needs you"
	Running   = "running"
	Parked    = "parked"
	Cleanable = "cleanable"
	Pool      = "pool"
)

// LaneStatus is what the dashboard and aw recap show for one lane.
type LaneStatus struct {
	Number   int             `json:"number"`
	Dir      string          `json:"lane"`
	Name     string          `json:"name,omitempty"`
	Purpose  string          `json:"purpose,omitempty"`
	State    string          `json:"state"`
	Section  string          `json:"section"`
	Why      string          `json:"why"`
	Since    string          `json:"since,omitempty"` // when the lane last changed state
	Sessions []SessionStatus `json:"sessions,omitempty"`
	Repos    []RepoStatus    `json:"repos,omitempty"`
	Handoff  *HandoffStatus  `json:"handoff,omitempty"`
}

// SessionStatus is a session as observed now: an active session whose process is gone shows as
// interrupted, though nothing is written.
type SessionStatus struct {
	Harness      string `json:"harness"`
	ID           string `json:"id"`
	State        string `json:"state"`
	LastActivity string `json:"last_activity"`
}

// RepoStatus is one repository of a lane.
type RepoStatus struct {
	Project  string        `json:"project"`
	Branch   string        `json:"branch,omitempty"`
	Head     string        `json:"head,omitempty"`
	Dirty    int           `json:"dirty"`
	Ahead    int           `json:"ahead"`  // of the base
	Push     string        `json:"push"`   // pushed, N ahead of <upstream>, not pushed, unknown
	Proven   bool          `json:"proven"` // proven merged; false is not proven, or not tried (see Status)
	Error    string        `json:"error,omitempty"`
	Remote   *forge.Change `json:"remote,omitempty"`
	upstream string
}

// HandoffStatus is a lane's latest handoff.
type HandoffStatus struct {
	Summary string `json:"summary"`
	Plan    string `json:"plan,omitempty"`
	Created string `json:"created"`
	Stale   bool   `json:"stale"`   // a head moved since the handoff
	Invalid bool   `json:"invalid"` // the record cannot be read: needs inspection
}

// StatusOptions configure Status.
type StatusOptions struct {
	Only   int  // one lane's number, or 0 for all
	Remote bool // also ask the forge (aw recap --remote)
}

// Status computes every lane's status, reading only: Git is asked for each repository in
// parallel, and nothing is recorded.
func (m *Manager) Status(ctx context.Context, o StatusOptions) ([]*LaneStatus, error) {
	lanes, err := state.Lanes(m.Store.DB())
	if err != nil {
		return nil, err
	}
	sessions, err := state.Sessions(m.Store.DB())
	if err != nil {
		return nil, err
	}
	var out []*LaneStatus
	for _, l := range lanes {
		if o.Only != 0 && l.Number != o.Only {
			continue
		}
		ls := &LaneStatus{Number: l.Number, Dir: Dir(l.Number, m.Cfg.Lanes.Max), Name: l.Name, Purpose: l.Purpose, State: l.State, Since: l.Updated}
		for _, s := range sessions {
			if s.Lane != l.Number || s.LaneGeneration != l.Generation {
				continue
			}
			st := s.State
			if st == state.SessionActive && !Live(s) {
				st = state.SessionInterrupted
			}
			ls.Sessions = append(ls.Sessions, SessionStatus{Harness: s.Harness, ID: s.HarnessSession, State: st, LastActivity: s.LastActivity})
		}
		out = append(out, ls)
	}

	// The repositories, in parallel.
	type job struct {
		ls *LaneStatus
		r  state.LaneRepo
		i  int
	}
	var jobs []job
	for _, ls := range out {
		if ls.State == state.LanePool || ls.State == state.LaneClaiming {
			continue
		}
		repos, err := state.LaneRepos(m.Store.DB(), ls.Number)
		if err != nil {
			return nil, err
		}
		ls.Repos = make([]RepoStatus, len(repos))
		for i, r := range repos {
			jobs = append(jobs, job{ls, r, i})
		}
	}
	var deadline time.Time
	if o.Remote {
		deadline = time.Now().Add(5 * time.Second)
	}
	var mu sync.Mutex
	clients := map[string]*forge.Client{}
	parallel(jobs, func(j job) {
		rs := m.repoStatus(ctx, j.r)
		if o.Remote && rs.Error == "" {
			mu.Lock()
			rs.Remote = m.remoteStatus(ctx, j.r, rs.Head, clients, deadline)
			mu.Unlock()
		}
		j.ls.Repos[j.i] = rs
	})

	// The merge proof costs several Git calls per repository, and a lane is cleanable only when
	// every repository is proven. So a dirty or unreadable repository rules its lane out without a
	// proof, and within a lane the repositories are proven one at a time, those with commits ahead
	// of the base first (the likeliest to fail), stopping at the first that is not proven. Proven
	// therefore means proven; false means not proven or not tried. Lanes run in parallel.
	type laneJob struct {
		ls   *LaneStatus
		jobs []job
	}
	var laneJobs []laneJob
	for _, ls := range out {
		ok := len(ls.Repos) > 0
		for _, r := range ls.Repos {
			if r.Dirty > 0 || r.Error != "" || r.Head == "" {
				ok = false
			}
		}
		if !ok {
			continue
		}
		var mine []job
		for _, j := range jobs {
			if j.ls == ls {
				mine = append(mine, j)
			}
		}
		sort.SliceStable(mine, func(a, b int) bool { return ls.Repos[mine[a].i].Ahead > ls.Repos[mine[b].i].Ahead })
		laneJobs = append(laneJobs, laneJob{ls, mine})
	}
	parallel(laneJobs, func(c laneJob) {
		for _, j := range c.jobs {
			ev := evidence.Check(ctx, j.r.Path, j.r.BaseRef, m.evidenceOptions(j.r))
			if j.ls.Repos[j.i].Proven = ev.Clean && ev.Proof != ""; !j.ls.Repos[j.i].Proven {
				return
			}
		}
	})

	for _, ls := range out {
		l, _ := state.LaneByNumber(m.Store.DB(), ls.Number)
		m.classify(ls, l)
	}
	return out, nil
}

// parallel runs f on every item, at most eight at a time: each starts Git processes.
func parallel[T any](items []T, f func(T)) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			f(it)
		}()
	}
	wg.Wait()
}

// repoStatus reads one worktree with one git status call: branch, head, upstream, ahead and dirty.
func (m *Manager) repoStatus(ctx context.Context, r state.LaneRepo) RepoStatus {
	rs := RepoStatus{Project: repoKey(r), Push: "unknown"}
	out, err := run.In(r.Path).Run(ctx, "--no-optional-locks", "status", "--porcelain=v2", "--branch", "-unormal")
	if err != nil {
		rs.Error = "cannot be read"
		return rs
	}
	ab := ""
	// Copies aw made when it built the lane, still as it wrote them, are not the lane's work.
	var copies []string
	if r.Project == "" || m.relOf(r) == "." {
		copies = workspace.UnchangedMaterialized(r.Path)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "1 .M ") && slices.Contains(copies, statusPath(line)),
			strings.HasPrefix(line, "? ") && slices.Contains(copies, strings.TrimPrefix(line, "? ")):
			// not counted
		case strings.HasPrefix(line, "# branch.oid "):
			rs.Head = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.head "):
			if b := strings.TrimPrefix(line, "# branch.head "); b != "(detached)" {
				rs.Branch = b
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			rs.upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			ab = strings.TrimPrefix(line, "# branch.ab ")
		case line != "" && !strings.HasPrefix(line, "#"):
			rs.Dirty++
		}
	}
	g := run.In(r.Path)
	switch {
	case rs.upstream == "":
		// No upstream configured. A commit some remote branch already has is pushed, such as a lane
		// branch that never moved from its base; a push without -u still leaves origin's ref.
		ref := "refs/remotes/origin/" + rs.Branch
		if n, err := g.Line(ctx, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil && n == "0" {
			rs.Push = "pushed"
		} else if rs.Branch == "" || !ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", ref)) {
			rs.Push = "not pushed"
		} else if n, err := g.Line(ctx, "rev-list", "--count", ref+"..HEAD"); err == nil {
			rs.Push = pushState(n, "origin/"+rs.Branch)
		}
	case ab != "":
		var ahead, behind int
		fmt.Sscanf(ab, "+%d -%d", &ahead, &behind)
		rs.Push = pushState(strconv.Itoa(ahead), rs.upstream)
	}
	rs.Ahead = ownCommits(ctx, g, r)
	return rs
}

// ownCommits counts the lane's own commits: those on its head that are neither where it started nor
// on the base branch. Commits the starting point already had, such as ones the owner had not pushed,
// are not the lane's, and neither are the base's commits the lane merged in since.
// One Git call in the usual case; a base ref that is gone, such as a pruned upstream, costs a retry.
func ownCommits(ctx context.Context, g run.Git, r state.LaneRepo) int {
	var refs []string
	for _, ref := range []string{r.BaseSHA, r.BaseRef} {
		if ref != "" {
			refs = append(refs, "^"+ref)
		}
	}
	for len(refs) > 0 {
		if out, err := g.Line(ctx, append([]string{"rev-list", "--count", "HEAD"}, refs...)...); err == nil {
			n, _ := strconv.Atoi(out)
			return n
		}
		refs = refs[:len(refs)-1] // without the base ref; the starting commit is always there
	}
	return 0
}

// statusPath is the path of an ordinary changed entry in git status --porcelain=v2.
func statusPath(line string) string {
	fields := strings.SplitN(line, " ", 9)
	if len(fields) < 9 {
		return ""
	}
	return fields[8]
}

func pushState(ahead, upstream string) string {
	if ahead == "0" {
		return "pushed"
	}
	return ahead + " ahead of " + upstream
}

func (m *Manager) remoteStatus(ctx context.Context, r state.LaneRepo, head string, clients map[string]*forge.Client, deadline time.Time) *forge.Change {
	if time.Now().After(deadline) {
		ch := forge.Change{Availability: "unknown (deadline)"}
		return &ch
	}
	remote, target := "origin", strings.TrimPrefix(r.BaseRef, "refs/heads/")
	if rest, ok := strings.CutPrefix(r.BaseRef, "refs/remotes/"); ok {
		remote, target, _ = strings.Cut(rest, "/")
	}
	raw, err := run.In(filepath.Join(m.WS.Owner, m.relOf(r))).Line(ctx, "config", "--get", "remote."+remote+".url")
	if err != nil {
		return nil
	}
	host, project, ok := forge.ParseRemote(raw)
	if !ok {
		return nil
	}
	c, seen := clients[host]
	if !seen {
		c = forge.For(m.Cfg.Workspace.Forge, host, deadline)
		clients[host] = c
	}
	if c == nil {
		return nil
	}
	ch := c.Find(ctx, forge.Identity{Host: host, Project: project, Target: target}, r.Branch, head)
	if time.Now().After(deadline) && !strings.HasPrefix(ch.Availability, forge.Verified) {
		ch = forge.Change{Availability: "unknown (deadline)"}
	}
	return &ch
}

// setupProgress says where a lane's setup has got to (R5): the step failing or running, and how far along.
func (m *Manager) setupProgress(l state.Lane) string {
	steps, err := state.Steps(m.Store.DB(), l.Number, l.Generation)
	if err != nil || len(steps) == 0 {
		return ""
	}
	finished, current := 0, ""
	for _, s := range steps {
		switch {
		case s.Finished():
			finished++
		case s.Status == state.StepFailed:
			return fmt.Sprintf(" (%s failed) → aw setup %s", s.Name, l.Name)
		case s.Status == state.StepRunning && current == "":
			current = s.Name
		}
	}
	if current == "" {
		return fmt.Sprintf(" (%d/%d)", finished, len(steps))
	}
	return fmt.Sprintf(" (%s, %d/%d)", current, finished, len(steps))
}

// classify puts a lane in a dashboard section and says why.
func (m *Manager) classify(ls *LaneStatus, l state.Lane) {
	running := false
	interrupted := false
	for _, s := range ls.Sessions {
		switch s.State {
		case state.SessionActive:
			running = true
		case state.SessionInterrupted:
			interrupted = true
		}
	}
	if h, err := state.LatestHandoff(m.Store.DB(), l.Number, l.Generation); err == nil && h.Archived != "" {
		// A later session has picked it up: it no longer describes the lane.
	} else if err == nil {
		hs := &HandoffStatus{Summary: h.Summary, Plan: h.Plan, Created: h.Created}
		var heads map[string]string
		if json.Unmarshal([]byte(h.Heads), &heads) != nil || len(heads) == 0 {
			hs.Invalid = true
		} else {
			for _, r := range ls.Repos {
				if r.Head != "" && heads[r.Project] != r.Head {
					hs.Stale = true
				}
			}
		}
		ls.Handoff = hs
	} else if !errors.Is(err, state.ErrNotFound) {
		ls.Handoff = &HandoffStatus{Invalid: true}
	}
	proven := len(ls.Repos) > 0
	for _, r := range ls.Repos {
		if !r.Proven {
			proven = false
		}
	}
	switch {
	case ls.State == state.LanePool:
		ls.Section, ls.Why = Pool, "warm"
	case ls.State == state.LaneClaiming || ls.State == state.LaneSettingUp:
		ls.Section, ls.Why = Running, "setting up"+m.setupProgress(l)
	case ls.State == state.LaneIncomplete:
		ls.Section, ls.Why = NeedsYou, "setup incomplete"+m.setupProgress(l)
	case ls.Handoff != nil && ls.Handoff.Invalid:
		ls.Section, ls.Why = NeedsYou, "handoff needs inspection (it cannot be read)"
	case ls.State == state.LaneParked:
		ls.Section, ls.Why = Parked, "parked"
	case running:
		ls.Section, ls.Why = Running, "session running"
	case proven:
		ls.Section, ls.Why = Cleanable, "proven merged"
	case ls.State == state.LaneReady && ls.Handoff != nil && ls.Handoff.Stale:
		ls.Section, ls.Why = NeedsYou, "ready for review, but changed since the handoff"
	case ls.State == state.LaneReady:
		ls.Section, ls.Why = NeedsYou, "ready for review"
	case interrupted:
		ls.Section, ls.Why = NeedsYou, "interrupted"
	default:
		ls.Section, ls.Why = NeedsYou, "no session attached"
	}
}
