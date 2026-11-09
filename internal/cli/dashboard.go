package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/plans"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// openForRead opens the workspace for a read-only view: the configuration from the cache or the
// files, and the state read-only. It writes nothing, and works before any state exists.
func openForRead() (*lanes.Manager, func(), error) {
	ws, cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	dir, err := ws.StateDir()
	if err != nil {
		return nil, nil, err
	}
	store, err := state.Open(dir, state.ReadOnly)
	if errors.Is(err, state.ErrNoState) {
		return &lanes.Manager{WS: ws, Cfg: cfg}, func() {}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &lanes.Manager{WS: ws, Cfg: cfg, Store: store}, func() { store.Close() }, nil
}

func statuses(cmd *cobra.Command, m *lanes.Manager, o lanes.StatusOptions) ([]*lanes.LaneStatus, error) {
	if m.Store == nil {
		return []*lanes.LaneStatus{}, nil
	}
	out, err := m.Status(cmd.Context(), o)
	if out == nil {
		out = []*lanes.LaneStatus{}
	}
	return out, err
}

// dashboard is aw with no arguments: what needs you, what is running, what is parked, and what can
// be cleaned up. It reads local state only (R81).
func dashboard(cmd *cobra.Command, asJSON bool) error {
	cwd, _ := os.Getwd()
	if _, err := workspace.Find(cwd); err != nil {
		var nf *workspace.NotFoundError
		if errors.As(err, &nf) && !asJSON {
			fmt.Fprintln(cmd.OutOrStdout(), "Not inside an Agent Workspace. Create one with aw init, or cd into one.")
			fmt.Fprintln(cmd.OutOrStdout())
			return cmd.Help()
		}
		return err
	}
	m, done, err := openForRead()
	if err != nil {
		return err
	}
	defer done()
	all, err := statuses(cmd, m, lanes.StatusOptions{})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	pairs, err := pairSessions(m)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, map[string]any{"lanes": all, "pair_sessions": pairs})
	}
	width := ui.Width(os.Stdout)
	printSections(out, all, width, false)
	printPairs(out, m.WS.Owner, pairs, width)
	return nil
}

// pairSessions are the live pair sessions, or none before setup.
func pairSessions(m *lanes.Manager) ([]state.Session, error) {
	if m.Store == nil {
		return []state.Session{}, nil
	}
	out, err := state.PairSessions(m.Store.DB())
	if out == nil {
		out = []state.Session{}
	}
	return out, err
}

// ownerBranch is the owner checkout's branch, read directly, or "".
func ownerBranch(owner string) string {
	data, err := os.ReadFile(filepath.Join(owner, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if branch == strings.TrimSpace(string(data)) {
		return "" // a detached HEAD has no name to show
	}
	return branch
}

func printPairs(out io.Writer, owner string, pairs []state.Session, width int) {
	if len(pairs) == 0 {
		return
	}
	fmt.Fprintln(out, ui.Fit("PAIR SESSIONS (in the owner checkout)", width))
	for _, s := range pairs {
		purpose := s.Purpose
		if purpose == "" { // no prompt seen yet: the branch says what it is working on (ux.md)
			purpose = ownerBranch(owner)
		}
		fmt.Fprintln(out, ui.Fit(fmt.Sprintf("  %-16s started %-10s %s", ui.Clean(s.Harness), ago(s.Started), quoted(purpose)), width))
	}
}

func printSections(out io.Writer, all []*lanes.LaneStatus, width int, detail bool) {
	line := func(s string) { fmt.Fprintln(out, ui.Fit(s, width)) }
	section := func(title, which string) {
		var these []*lanes.LaneStatus
		for _, ls := range all {
			if ls.Section == which {
				these = append(these, ls)
			}
		}
		line(title)
		if len(these) == 0 {
			line("  (none)")
			return
		}
		for _, ls := range these {
			why := ls.Why
			if ls.Section == lanes.Parked {
				why = "parked " + ago(ls.Since)
			}
			summary := repoSummary(ls)
			if last := lastActivity(ls); last != "" && ls.Section == lanes.NeedsYou {
				summary = strings.TrimPrefix(summary+"  last "+last, "  ")
			}
			line(fmt.Sprintf("  %-16s %-20s %-30s %s", ui.Clean(ls.Name), why, summary, quoted(ls.Purpose)))
			if !detail {
				continue
			}
			if h := ls.Handoff; h != nil && !h.Invalid {
				limit := 0 // no known width: in full
				if width > 0 {
					limit = max(20, width-30)
				}
				note := "handoff: " + quoted(ui.Brief(h.Summary, limit))
				if h.Stale {
					note += " (stale: the work changed since)"
				}
				line("  " + strings.Repeat(" ", 17) + note)
			}
			for _, r := range ls.Repos {
				if r.Error != "" {
					line(fmt.Sprintf("  %17s%s: %s", "", r.Project, r.Error))
					continue
				}
				remote := ""
				if r.Remote != nil {
					remote = ", remote: " + r.Remote.Availability
					if r.Remote.State != "" {
						remote = fmt.Sprintf(", remote: !%d %s", r.Remote.IID, r.Remote.State)
					}
				}
				line(fmt.Sprintf("  %17s%s %s: %s%s", "", r.Project, short(r.Head), r.Push, remote))
			}
		}
	}
	section("NEEDS YOU", lanes.NeedsYou)
	section("RUNNING", lanes.Running)
	section("PARKED", lanes.Parked)
	var cleanable []string
	pool := 0
	for _, ls := range all {
		switch ls.Section {
		case lanes.Cleanable:
			cleanable = append(cleanable, ui.Clean(ls.Name))
		case lanes.Pool:
			pool++
		}
	}
	line("CLEANABLE")
	switch n := len(cleanable); {
	case n == 0:
		line("  (none)")
	case detail:
		line(fmt.Sprintf("  %d lane%s proven merged → aw sweep: %s", n, plural(n), strings.Join(cleanable, ", ")))
	default:
		line(fmt.Sprintf("  %d lane%s proven merged → aw sweep", n, plural(n)))
	}
	if pool > 0 {
		line(fmt.Sprintf("WARM POOL: %d lane%s ready to claim", pool, plural(pool)))
	}
}

func repoSummary(ls *lanes.LaneStatus) string {
	var parts []string
	for _, r := range ls.Repos {
		switch {
		case r.Error != "":
			parts = append(parts, r.Project+": unreadable")
		case r.Dirty > 0 || r.Ahead > 0:
			var bits []string
			if r.Dirty > 0 {
				bits = append(bits, fmt.Sprintf("%d dirty", r.Dirty))
			}
			if r.Ahead > 0 {
				bits = append(bits, fmt.Sprintf("%d ahead", r.Ahead))
			}
			parts = append(parts, r.Project+": "+strings.Join(bits, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// lastActivity is when the lane's sessions were last seen, in local time: 14:02 today, or the date.
func lastActivity(ls *lanes.LaneStatus) string {
	var last time.Time
	for _, s := range ls.Sessions {
		if t, err := time.Parse(time.RFC3339Nano, s.LastActivity); err == nil && t.After(last) {
			last = t
		}
	}
	if last.IsZero() {
		return ""
	}
	last = last.Local()
	if y, m, d := time.Now().Date(); last.Year() == y && last.Month() == m && last.Day() == d {
		return last.Format("15:04")
	}
	return last.Format("Jan 2")
}

func quoted(s string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("%q", ui.Clean(s))
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ago(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func newRecapCmd() *cobra.Command {
	var asJSON, remote, all bool
	cmd := &cobra.Command{
		Use:   "recap [--remote]",
		Short: "Full status, for after a reboot or a weekend",
		Long: `Show every lane in full: what needs you, what is running, what is parked and what can be cleaned
up, with each lane's handoff and each repository's head and push state. In a lane it shows that
lane only, unless --all. It reads local state only; --remote also asks the forge for each change's
state, within a deadline. It writes nothing.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			m, done, err := openForRead()
			if err != nil {
				return err
			}
			defer done()
			o := lanes.StatusOptions{Remote: remote}
			if !all {
				o.Only = m.CallerLane()
			}
			ss, err := statuses(cmd, m, o)
			if err != nil {
				return err
			}
			var pairs []state.Session
			if o.Only == 0 { // a lane's own recap is about that lane
				if pairs, err = pairSessions(m); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.Marshal(map[string]any{"lanes": ss, "pair_sessions": pairs, "efforts": efforts(m.WS, ss)})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, string(data))
				return err
			}
			printSections(out, ss, ui.Width(os.Stdout), true)
			printPairs(out, m.WS.Owner, pairs, ui.Width(os.Stdout))
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the status as one line of JSON")
	cmd.Flags().BoolVar(&remote, "remote", false, "also show each change's state on the forge")
	cmd.Flags().BoolVar(&all, "all", false, "in a lane, show every lane, not only this one")
	return cmd
}

// effort is one plan's work across lanes: a plan several lanes hand off to is one line of attention.
type effort struct {
	Identity  string `json:"identity"`
	Lanes     []int  `json:"lanes"`
	State     string `json:"state"`
	NextStep  string `json:"next_step"`
	Attention string `json:"attention"`
}

// efforts groups the lanes' handoffs by the plan they name. A plan's own state says whether it is
// finished: merging its lanes does not (R82).
func efforts(ws *workspace.Workspace, ss []*lanes.LaneStatus) []effort {
	byPlan := map[string]*effort{}
	var order []string
	for _, s := range ss {
		if s.Handoff == nil || s.Handoff.Plan == "" {
			continue
		}
		id := "plan:" + s.Handoff.Plan
		e, seen := byPlan[id]
		if !seen {
			e = &effort{Identity: id}
			byPlan[id] = e
			order = append(order, id)
			for _, dir := range []string{ws.Owner, filepath.Join(ws.Owner, ".worktrees", s.Dir)} {
				if p, err := plans.Parse(filepath.Join(dir, filepath.FromSlash(s.Handoff.Plan))); err == nil {
					e.State, e.NextStep = p.Fields["State"], p.Fields["Next step"]
					break
				}
			}
			switch {
			case strings.HasPrefix(strings.ToLower(e.NextStep), "the owner"):
				e.Attention = "Your action"
			case e.State == "FULLY VERIFIED":
				e.Attention = "Done"
			default:
				e.Attention = "Verify"
			}
		}
		e.Lanes = append(e.Lanes, s.Number)
	}
	out := make([]effort, 0, len(order))
	for _, id := range order {
		out = append(out, *byPlan[id])
	}
	return out
}
