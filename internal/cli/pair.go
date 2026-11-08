package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/harness"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/policy"
	"github.com/connor981332/agent-workspace/internal/state"
)

// agentContext says why this process is an agent's, or "" (S10): a harness's environment marker,
// AW_SESSION, or a known harness among its ancestors. Reading the ancestry costs a few milliseconds,
// so only owner-only commands do it.
func agentContext() string {
	if os.Getenv("AW_SESSION") != "" {
		return "AW_SESSION is set"
	}
	for _, e := range harnessEnv {
		if os.Getenv(e.env) != "" {
			return e.env + " is set"
		}
	}
	for _, h := range harness.Harnesses {
		if platform.HarnessAncestor(os.Getpid(), h) > 0 {
			return "it runs under " + h
		}
	}
	return ""
}

// refuseAgent refuses an owner-only command in agent context, for a harness without hooks or a
// command the guard did not see (S10). Delegations (M9) will be able to cover a command.
func refuseAgent(cmd *cobra.Command) error {
	if cmd.Parent() == nil || cmd.Parent().Parent() != nil {
		return nil // aw alone, or a subcommand group's member
	}
	name := cmd.Name()
	sweeping := false
	if name == "sweep" {
		apply, _ := cmd.Flags().GetBool("apply")
		retry, _ := cmd.Flags().GetBool("retry")
		sweeping = apply || retry
	}
	if !policy.OwnerOnly(name) && !sweeping {
		return nil
	}
	if why := agentContext(); why != "" {
		return &commandError{fmt.Errorf("aw %s is only for the human, and this looks like an agent session (%s) → ask the human to run it in their own terminal", name, why)}
	}
	return nil
}

func newPairCmd() *cobra.Command {
	var harnessName, session string
	cmd := &cobra.Command{
		Use:   "pair [--harness h] [--session harness:id] [-- harness arguments…]",
		Short: "Invite an agent to work with you in the owner checkout",
		Long: `Start a harness in the owner checkout as a pair session: it may edit there, and no lane is claimed.
Destructive Git, publication beyond the policy, and changes to the workspace's own configuration
stay refused. With --session, grant a session already running in the owner checkout instead; a
session that is refused a write prints the exact command. Only you can grant pairing.`,
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if dash := cmd.ArgsLenAtDash(); dash != 0 && len(args) > 0 {
				return &usageError{errors.New("aw pair takes no arguments; put the harness's arguments after --")}
			}
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			if w.ws.LaneRoot != "" {
				return fmt.Errorf("aw pair is for the owner checkout, and this is the lane at %s → run it from %s", w.ws.LaneRoot, w.ws.Owner)
			}
			others, err := state.PairSessions(w.store.DB())
			if err != nil {
				return err
			}
			if n := len(others); n > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "aw: warning: %d other pair session%s running in the owner checkout; agents editing one tree can collide, which is what lanes prevent\n", n, plural(n))
			}
			if session != "" {
				if harnessName != "" || len(args) > 0 {
					return &usageError{errors.New("--session grants a running session, so it takes no --harness or harness arguments")}
				}
				return grantPair(cmd.Context(), w.store, session, cmd)
			}
			if harnessName == "" {
				harnessName = w.cfg.User.Harness
			}
			if err := lanes.CheckName(harnessName); err != nil {
				return fmt.Errorf("%q is not a harness name", harnessName)
			}
			path, err := exec.LookPath(harnessName)
			if err != nil {
				return fmt.Errorf("the harness %s is not installed (not on PATH) → install it, or choose another with --harness or [user] harness", harnessName)
			}
			id := "pair-" + randomHex(8)
			pid := os.Getpid()
			start, err := platform.ProcessIdentity(pid)
			if err != nil {
				return err
			}
			if err := savePair(cmd.Context(), w.store, state.Session{Harness: harnessName, HarnessSession: id, PID: pid, PIDStart: start}); err != nil {
				return err
			}
			owner := w.ws.Owner
			w.Close()
			if err := os.Chdir(owner); err != nil {
				return err
			}
			env := append(os.Environ(), "AW_SESSION="+harnessName+":"+id, fmt.Sprintf("AW_SESSION_PID=%d", pid), "PWD="+owner)
			// The harness replaces this process, so it keeps the PID just recorded.
			return syscall.Exec(path, append([]string{harnessName}, args...), env)
		}),
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "the harness to launch (default: [user] harness)")
	cmd.Flags().StringVar(&session, "session", "", "grant this running session, as harness:id")
	return cmd
}

func savePair(ctx context.Context, store *state.Store, s state.Session) error {
	now := state.Now()
	s.Pair, s.State, s.Started, s.LastActivity = true, state.SessionActive, now, now
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := state.SaveSession(tx, s)
		return err
	})
}

// grantPair makes a running session a pair session. Its process is not known here; its next hook
// call records it. A session working in a lane stays there: its lane is not given up by accident.
func grantPair(ctx context.Context, store *state.Store, session string, cmd *cobra.Command) error {
	h, id, ok := strings.Cut(session, ":")
	if !ok || id == "" || !slices.Contains(harness.Harnesses, h) {
		return &usageError{fmt.Errorf("--session takes <harness>:<session id>, with a harness among %v", harness.Harnesses)}
	}
	rec, err := state.SessionByHarnessID(store.DB(), h, id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		rec.HarnessSession = id
	case err != nil:
		return err
	case rec.Lane > 0 && rec.State == state.SessionActive && rec.Alive():
		return fmt.Errorf("session %s is attached to a lane, so it stays there → finish that work first, or start a new session with aw pair", session)
	}
	if err := savePair(ctx, store, state.Session{Harness: h, HarnessSession: rec.HarnessSession}); err != nil {
		return err
	}
	// Processes from an earlier life of this record must not count: its next hook call records the
	// one that is running now.
	if err := store.Write(ctx, func(tx *sql.Tx) error { return state.ForgetSessionProcesses(tx, h, rec.HarnessSession) }); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Session %s is now a pair session in the owner checkout.\n", session)
	return nil
}
