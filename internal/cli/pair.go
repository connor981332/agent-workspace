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

	gitrun "github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/harness"
	"github.com/connor981332/agent-workspace/internal/harness/launch"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/mux"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/policy"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// agentContext says why this process is an agent's, or "" (S10): a harness's environment marker,
// AW_SESSION, or a known harness among its ancestors. Reading the ancestry costs a few milliseconds,
// so only owner-only commands do it.
func agentContext() string {
	if inRehearsal() {
		return "" // a throwaway copy, where aw rehearse runs the human's commands against nothing real
	}
	if s := os.Getenv("AW_SESSION"); s != "" && !strings.HasPrefix(s, shellHarness+":") {
		return "AW_SESSION is set" // aw new --shell sets it for a person's shell, which is no agent
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

// unguardedAgent is agentContext, for a command the hook has already had its say on: where aw's hook
// is installed, an agent reaches a human-only command only if the human approved it at the prompt.
// A harness without aw's hook, or a workspace not set up yet, has had no such check.
func unguardedAgent() string {
	why := agentContext()
	if why == "" {
		return ""
	}
	if cwd, err := os.Getwd(); err == nil {
		if ws, err := workspace.Find(cwd); err == nil && sandbox.HasAWHooks(ws.Owner) {
			return ""
		}
	}
	return why
}

func inRehearsal() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	ws, err := workspace.Find(cwd)
	return err == nil && workspace.IsRehearsal(ws.Owner)
}

// refuseAgent refuses an owner-only command in agent context, for a harness without hooks or a
// command the guard did not see (S10). Delegations (M9) will be able to cover a command.
func refuseAgent(cmd *cobra.Command, args []string) error {
	if cmd.Parent() == nil || cmd.Parent().Parent() != nil {
		return nil // aw alone, or a subcommand group's member
	}
	name := cmd.Name()
	sweeping := false
	if name == "sweep" {
		apply, _ := cmd.Flags().GetBool("apply")
		retry, _ := cmd.Flags().GetInt("retry")
		sweeping = apply || retry > 0
	}
	if name == "archives" {
		restore, _ := cmd.Flags().GetString("restore")
		sweeping = restore != "" // listing only reads; restoring makes branches in the owner's repositories
	}
	if !policy.OwnerOnly(name) && !sweeping {
		return nil
	}
	if name == "rehearse" {
		return nil // the command itself checks: an agent may rehearse its own onboarding lane
	}
	if name == "setup" && len(args) == 1 {
		return nil // retrying a lane's setup: the command itself checks that it is the agent's own lane
	}
	if name == "delegate" {
		if status, _ := cmd.Flags().GetBool("status"); status {
			return nil
		}
		if revoke, _ := cmd.Flags().GetBool("revoke"); revoke {
			return nil
		}
	}
	if why := unguardedAgent(); why != "" {
		if delegatedHere(name, args) {
			return nil
		}
		return &commandError{fmt.Errorf("aw %s is only for the human, and this looks like an agent session (%s) → ask the human to run it in their own terminal", name, why)}
	}
	return nil
}

func newPairCmd() *cobra.Command {
	var harnessName, session string
	var useMux bool
	cmd := &cobra.Command{
		Use:   "pair [--harness h] [--mux] [--session harness:id] [-- harness arguments…]",
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
				if harnessName != "" || len(args) > 0 || useMux {
					return &usageError{errors.New("--session grants a running session, so it takes no --harness, --mux or harness arguments")}
				}
				return grantPair(cmd.Context(), w, session, cmd)
			}
			return startPairSession(cmd, w, harnessName, useMux, args)
		}),
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "the harness to launch (default: [user] harness)")
	cmd.Flags().StringVar(&session, "session", "", "grant this running session, as harness:id")
	cmd.Flags().BoolVar(&useMux, "mux", false, "open it in your terminal multiplexer")
	return cmd
}

// startPairSession replaces this process with the harness, in the owner checkout. It returns only
// when something stopped that.
func startPairSession(cmd *cobra.Command, w *writeContext, harnessName string, useMux bool, args []string) error {
	var err error
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
	if useMux {
		if err := mux.Check(w.cfg.User.Multiplexer); err != nil {
			return err
		}
	}
	id := "pair-" + randomHex(8)
	pid, start := os.Getpid(), ""
	if useMux {
		pid = 0 // the harness's own process is recorded by its first hook call
	} else if start, err = platform.ProcessIdentity(pid); err != nil {
		return err
	}
	if err := savePair(cmd.Context(), w.store, state.Session{Harness: harnessName, HarnessSession: id, PID: pid, PIDStart: start}); err != nil {
		return err
	}
	args = append(launch.SandboxArgs(harnessName, manager(w).SandboxRoots("")), args...)
	owner, mx := w.ws.Owner, w.cfg.User.Multiplexer
	w.Close()
	if useMux {
		return openInMux(cmd, mx, "pair", owner, harnessName, id, append([]string{harnessName}, args...))
	}
	if err := os.Chdir(owner); err != nil {
		return err
	}
	env := append(os.Environ(), "AW_SESSION="+harnessName+":"+id, fmt.Sprintf("AW_SESSION_PID=%d", pid), "PWD="+owner)
	// The harness replaces this process, so it keeps the PID just recorded.
	return syscall.Exec(path, append([]string{harnessName}, args...), env)
}

func savePair(ctx context.Context, store *state.Store, s state.Session) error {
	now := state.Now()
	s.Pair, s.State, s.Started, s.LastActivity = true, state.SessionActive, now, now
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := state.SaveSession(tx, s)
		return err
	})
}

// grantPair makes a running session a pair session (docs/spec/sessions.md): it always succeeds, since
// only the human runs it. A lane the session held is released, and its work stays there; when that
// work is uncommitted, a warning says so. The session's process is not known here; its next hook
// call records it.
func grantPair(ctx context.Context, w *writeContext, session string, cmd *cobra.Command) error {
	store := w.store
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
	}
	var left *state.Lane
	if rec.Lane > 0 {
		if l, err := state.LaneByNumber(store.DB(), rec.Lane); err == nil && rec.InLane(l) {
			left = &l
		}
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
	if left != nil {
		if err := newBroker(w).ReleaseLane(ctx, left.Number); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "aw: warning: lane %s's seats could not be given back (%v) → aw stop releases them\n", ui.Clean(left.Name), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "It no longer holds lane %s.\n", ui.Clean(left.Name))
		if dirty := uncommitted(ctx, store, left.Number); len(dirty) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "aw: warning: lane %s has uncommitted work in %s, which stays in the lane → the pair session can still commit it there (cd %s)\n",
				ui.Clean(left.Name), strings.Join(dirty, ", "), shellQuote(manager(w).Root(left.Number)))
		}
	}
	return nil
}

// uncommitted names a lane's repositories that have uncommitted changes.
func uncommitted(ctx context.Context, store *state.Store, lane int) []string {
	repos, err := state.LaneRepos(store.DB(), lane)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range repos {
		status, err := gitrun.In(r.Path).Run(ctx, "--no-optional-locks", "status", "--porcelain=v1")
		if err != nil {
			continue
		}
		if status = workspace.WithoutMaterialized(r.Path, status); status != "" {
			name := r.Project
			if name == "" {
				name = "the workspace repository"
			}
			out = append(out, name)
		}
	}
	return out
}
