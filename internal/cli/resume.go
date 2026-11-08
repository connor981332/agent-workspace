package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/connor981332/agent-workspace/internal/harness/launch"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// resumeGrace is how soon a harness must fail for aw to conclude it could not resume the session,
// and open a new one instead. A later failure is the session's own, and is the human's to see.
const resumeGrace = 5 * time.Second

// candidate is a session that can be reopened: the newest one of its lane, or a pair session.
type candidate struct {
	session state.Session
	lane    *state.Lane // nil for a pair session
	label   string
}

// candidates are the sessions that are not running, newest first, one per lane.
func candidates(w *writeContext) ([]candidate, error) {
	sessions, err := state.Sessions(w.store.DB())
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []candidate
	pair := false
	for _, s := range sessions {
		running := s.State == state.SessionActive && s.Alive()
		if s.Pair {
			if !pair && !running {
				out = append(out, candidate{session: s, label: "pair"})
			}
			pair = true
			continue
		}
		if s.Lane == 0 || seen[s.Lane] {
			continue
		}
		seen[s.Lane] = true
		l, err := state.LaneByNumber(w.store.DB(), s.Lane)
		if err != nil || l.Generation != s.LaneGeneration || running {
			continue
		}
		out = append(out, candidate{session: s, lane: &l, label: l.Name})
	}
	return out, nil
}

func describe(c candidate) string {
	what := c.session.State
	if c.lane != nil && (c.lane.State == state.LaneReady || c.lane.State == state.LaneParked) {
		what = c.lane.State
	}
	purpose := c.session.Purpose
	if c.lane != nil {
		purpose = c.lane.Purpose
	}
	return fmt.Sprintf("%-16s %-8s %-12s %s", ui.Clean(c.label), ui.Clean(c.session.Harness), what, quoted(purpose))
}

func pick(cmd *cobra.Command, all []candidate) (candidate, error) {
	out := cmd.OutOrStdout()
	if len(all) == 0 {
		return candidate{}, errors.New("there are no sessions to resume → aw new starts one")
	}
	for i, c := range all {
		fmt.Fprintf(out, "%d. %s\n", i+1, describe(c))
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return candidate{}, &silentError{errors.New("name the session to resume: aw resume <name>")}
	}
	fmt.Fprint(out, "Resume which? ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(all) {
		return candidate{}, errors.New("no session was chosen")
	}
	return all[n-1], nil
}

func find(w *writeContext, all []candidate, name string) (candidate, error) {
	if name == "pair" {
		for _, c := range all {
			if c.lane == nil {
				return c, nil
			}
		}
		return candidate{}, errors.New("there is no pair session to resume → aw pair starts one")
	}
	l, err := manager(w).Resolve(name)
	if err != nil {
		return candidate{}, err
	}
	for _, c := range all {
		if c.lane != nil && c.lane.Number == l.Number {
			return c, nil
		}
	}
	return candidate{}, fmt.Errorf("lane %s has no session to resume, or its session is still running → aw open %s starts a new one", l.Name, l.Name)
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [name]",
		Short: "Reopen a session where it left off",
		Long: `Relaunch a session's harness in its lane, with the harness's own resume flag and the recorded session
ID. With no name, choose from a list. If the harness can no longer resume it, this falls back to
aw open: a new session on the same lane, given the latest handoff. "pair" names the pair session.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			if err := manager(w).Recover(cmd.Context()); err != nil {
				return err
			}
			all, err := candidates(w)
			if err != nil {
				return err
			}
			var c candidate
			if len(args) == 1 {
				c, err = find(w, all, args[0])
			} else {
				c, err = pick(cmd, all)
			}
			if err != nil {
				return err
			}
			if c.lane != nil && c.lane.State == state.LaneParked {
				return fmt.Errorf("lane %s is parked → aw unpark %s, then aw resume %s", c.lane.Name, c.lane.Name, c.lane.Name)
			}
			return resume(cmd, w, c)
		}),
	}
}

// neverIdentified reports a session whose harness never told aw its own ID: aw's made-up one is
// all there is, and no harness can resume that.
func neverIdentified(s state.Session) bool {
	for _, prefix := range []string{"new-", "pair-", "open-"} {
		if strings.HasPrefix(s.HarnessSession, prefix) {
			return true
		}
	}
	return false
}

func resume(cmd *cobra.Command, w *writeContext, c candidate) error {
	errOut := cmd.ErrOrStderr()
	s := c.session
	args, known := launch.ResumeArgs(s.Harness, s.HarnessSession)
	if !known || neverIdentified(s) {
		if c.lane == nil {
			return fmt.Errorf("this pair session cannot be resumed, because %s never reported its session ID → aw pair starts a new one", ui.Clean(s.Harness))
		}
		fmt.Fprintf(errOut, "aw: %s cannot resume this session, so this opens a new one on lane %s\n", ui.Clean(s.Harness), ui.Clean(c.lane.Name))
		return open(cmd, w, *c.lane, s.Harness)
	}
	if _, err := exec.LookPath(s.Harness); err != nil {
		return fmt.Errorf("the harness %s is not installed (not on PATH) → install it, or aw open starts a session with the default harness", ui.Clean(s.Harness))
	}
	dir := w.ws.Owner
	if c.lane != nil {
		dir = manager(w).Root(c.lane.Number)
	}
	// This process waits for the harness, and holds the session until it exits.
	start, err := platform.ProcessIdentity(os.Getpid())
	if err != nil {
		return err
	}
	if err := w.store.Write(cmd.Context(), func(tx *sql.Tx) error { return state.JoinSession(tx, s.ID, os.Getpid(), start) }); err != nil {
		return err
	}
	fmt.Fprintf(errOut, "aw: resuming %s in %s\n", ui.Clean(s.Harness), ui.Clean(dir))
	env := append(os.Environ(), "AW_SESSION="+s.Harness+":"+s.HarnessSession, fmt.Sprintf("AW_SESSION_PID=%d", os.Getpid()), "PWD="+dir)
	began := time.Now()
	code, err := runners.RunHost(dir, env, append([]string{s.Harness}, args...))
	if err != nil {
		return err
	}
	if code != 0 && time.Since(began) < resumeGrace && c.lane != nil {
		fmt.Fprintf(errOut, "aw: %s could not resume the session, so this opens a new one on lane %s\n", ui.Clean(s.Harness), ui.Clean(c.lane.Name))
		// This process joined the session above; the session it was resuming did not come back.
		if err := w.store.Write(cmd.Context(), func(tx *sql.Tx) error { return state.InterruptSession(tx, s.ID) }); err != nil {
			return err
		}
		return open(cmd, w, *c.lane, s.Harness)
	}
	if code != 0 {
		return &exitStatus{code}
	}
	return nil
}

func newOpenCmd() *cobra.Command {
	var harnessName string
	cmd := &cobra.Command{
		Use:   "open <name> [--harness h]",
		Short: "Start a fresh session on existing work, with the handoff injected",
		Long: `Start a new session on an existing lane, and give it the latest handoff as its first prompt, so that
it picks up from the written record. The lane goes back to work, and the handoff stays on record.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			if err := m.Recover(cmd.Context()); err != nil {
				return err
			}
			l, err := m.Resolve(args[0])
			if err != nil {
				return err
			}
			if harnessName == "" {
				harnessName = w.cfg.User.Harness
			}
			return open(cmd, w, l, harnessName)
		}),
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "the harness to launch (default: [user] harness)")
	return cmd
}

// openPrompt tells a new session what the last one left.
func openPrompt(l state.Lane, h *state.Handoff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are picking up existing work in lane %s: %s.", ui.Clean(l.Name), ui.Clean(l.Purpose))
	if h == nil {
		b.WriteString(" The last session left no handoff, so start from the lane's commits and uncommitted changes.")
	} else {
		fmt.Fprintf(&b, " The last session's handoff: %s", ui.Clean(h.Summary))
		if h.Plan != "" {
			fmt.Fprintf(&b, " Its plan is %s: read that plan's status first.", ui.Clean(h.Plan))
		}
	}
	b.WriteString(" Run bin/aw whoami to confirm where you are, then carry on.")
	return b.String()
}

func open(cmd *cobra.Command, w *writeContext, l state.Lane, harnessName string) error {
	if l.State == state.LaneParked {
		return fmt.Errorf("lane %s is parked → aw unpark %s first", l.Name, l.Name)
	}
	if l.State != state.LaneWorking && l.State != state.LaneReady {
		return fmt.Errorf("lane %s is %s, so it has no work to open", l.Name, l.State)
	}
	if err := lanes.CheckName(harnessName); err != nil {
		return fmt.Errorf("%q is not a harness name", harnessName)
	}
	path, err := exec.LookPath(harnessName)
	if err != nil {
		return fmt.Errorf("the harness %s is not installed (not on PATH) → install it, or choose another with --harness or [user] harness", harnessName)
	}
	sessions, err := state.Sessions(w.store.DB())
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Lane == l.Number && s.LaneGeneration == l.Generation && s.State == state.SessionActive && s.Alive() {
			return fmt.Errorf("lane %s already has a running session (%s) → finish it first, so that two agents do not share one lane", l.Name, ui.Clean(s.Harness))
		}
	}
	var handoff *state.Handoff
	if h, err := state.LatestHandoff(w.store.DB(), l.Number, l.Generation); err == nil {
		handoff = &h
	} else if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	id := "open-" + randomHex(8)
	if err := claimForOpen(cmd.Context(), w, l, handoff, harnessName, id); err != nil {
		return err
	}
	root := manager(w).Root(l.Number)
	fmt.Fprintf(cmd.ErrOrStderr(), "aw: opening lane %s with %s\n", ui.Clean(l.Name), harnessName)
	w.Close()
	if err := os.Chdir(root); err != nil {
		return err
	}
	env := append(os.Environ(), "AW_SESSION="+harnessName+":"+id, fmt.Sprintf("AW_SESSION_PID=%d", os.Getpid()), "PWD="+root)
	return syscall.Exec(path, append([]string{harnessName}, launch.PromptArgs(harnessName, openPrompt(l, handoff))...), env)
}

func claimForOpen(ctx context.Context, w *writeContext, l state.Lane, h *state.Handoff, harnessName, id string) error {
	pid := os.Getpid()
	start, err := platform.ProcessIdentity(pid)
	if err != nil {
		return err
	}
	now := state.Now()
	return w.store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := state.SaveSession(tx, state.Session{Harness: harnessName, HarnessSession: id, Lane: l.Number, LaneGeneration: l.Generation,
			Purpose: l.Purpose, State: state.SessionActive, Started: now, LastActivity: now, PID: pid, PIDStart: start}); err != nil {
			return err
		}
		l.State, l.Updated = state.LaneWorking, now
		if err := state.UpdateLane(tx, l); err != nil {
			return err
		}
		if h != nil {
			return state.ArchiveHandoff(tx, h.ID)
		}
		return nil
	})
}
