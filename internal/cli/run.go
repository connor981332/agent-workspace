package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/mux"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/timing"
)

// exitStatus makes Main exit with a command's own status, printing nothing more.
type exitStatus struct{ code int }

func (e *exitStatus) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <project> [command…]",
		Short: "Run a command in the project, in this lane, with the lane's environment",
		Long: `Run a command in the project: in a lane, in the lane's worktree of it, with the lane's
environment (AW_LANE, AW_LANE_NAME, AW_LANE_ROOT, AW_LANE_STATE, AW_PROJECT, AW_PROJECT_PATH,
AW_OWNER, AW_NAMESPACE, AW_HOSTNAME, AW_TOOL, and the project's env file). In the owner checkout,
in the owner's checkout of the project.

Arguments, standard input, the exit status and signals pass through unchanged. With no command it
runs the project's [projects.<p>] run.`,
		Args: cobra.MinimumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			m := manager(w)
			// Before Locate: the env file it reads is what prepare writes.
			if err := awaitSetup(cmd.Context(), w, m, args[0], nil, cmd.ErrOrStderr()); err != nil {
				w.Close()
				return err
			}
			where, err := m.Locate(args[0])
			if err != nil {
				w.Close()
				return err
			}
			argv := args[1:]
			if len(argv) == 0 {
				p := w.cfg.Projects[args[0]]
				if p.Run == "" {
					w.Close()
					return fmt.Errorf("give a command to run, or set [projects.%s] run", args[0])
				}
				argv = []string{"sh", "-c", p.Run}
			}
			if leases, err := state.Leases(w.store.DB()); err == nil {
				where.Env = append(where.Env, seatEnv(w.cfg, newBroker(w).Resources(), leases, m.CallerLane())...)
			}
			var d *runners.Devcontainer
			if w.cfg.Projects[args[0]].Runner == "devcontainer" {
				if d, err = devcontainerOf(w, args[0]); err != nil {
					w.Close()
					return err
				}
			}
			w.Close() // the command may run for hours; hold nothing open
			code, err := runProject(cmd.Context(), d, where.Dir, where.Env, argv)
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitStatus{code}
			}
			return nil
		}),
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newNewCmd() *cobra.Command {
	var harness, branch, role string
	var useMux bool
	cmd := &cobra.Command{
		Use:   `new "<purpose>" [--harness h] [--mux] [--branch b] [-- harness arguments…]`,
		Short: "Claim a lane and launch a harness in it",
		Long: `Claim a lane for the purpose (from the warm pool when there is one), then launch the harness in the
lane root: the individual layer's [user] harness, or --harness. Arguments after -- go to the harness
unchanged. The harness runs as this process, with AW_SESSION set, so the session is recorded
against the lane.`,
		Args: cobra.MinimumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			purpose, extra := args[0], []string{}
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				if dash != 1 {
					return &usageError{fmt.Errorf(`aw new takes one purpose before --; quote it: aw new "fix checkout tax"`)}
				}
				extra = args[1:]
			} else if len(args) > 1 {
				return &usageError{fmt.Errorf(`aw new takes one purpose; quote it, and put harness arguments after --`)}
			}
			t := timing.Start()
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			t.Mark("open")
			if harness == "" {
				harness = w.cfg.User.Harness
			}
			if err := lanes.CheckName(harness); err != nil {
				return fmt.Errorf("%q is not a harness name", harness)
			}
			path, err := exec.LookPath(harness)
			if err != nil {
				return fmt.Errorf("the harness %s is not installed (not on PATH) → install it, or choose another with --harness or [user] harness", harness)
			}
			if useMux {
				if err := mux.Check(w.cfg.User.Multiplexer); err != nil {
					return err
				}
			}
			id := "new-" + randomHex(8)
			m := manager(w)
			if err := checkRole(w.cfg, role, false); err != nil {
				return err
			}
			pid := os.Getpid()
			if useMux {
				pid = 0 // the harness's own process is recorded by its first hook call
			}
			c, err := m.Claim(cmd.Context(), lanes.ClaimRequest{Purpose: purpose, Branch: branch,
				Who: lanes.Identity{Harness: harness, Session: id, PID: pid}})
			if err != nil {
				return err
			}
			if err := applyRole(cmd.Context(), w, c, role); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "aw: lane %s %s, starting %s\n", c.Dir, c.Lane.Name, harness)
			if !slices.Contains(knownHarnesses, harness) && w.cfg.Harnesses[harness] == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "aw: warning: %s has no hooks here, so nothing confines it to the lane. It gets the instructions in AGENTS.md, and bin/aw is its way in (docs/harnesses.md)\n", harness)
			}
			mx := w.cfg.User.Multiplexer
			t.Mark("claim")
			startSetup(cmd.ErrOrStderr(), w, c)
			t.Mark("setup")
			w.Close()
			t.Mark("close")
			if timing.Debug() {
				t.Report(cmd.ErrOrStderr(), "new")
			}
			if useMux {
				return openInMux(cmd, mx, c.Lane.Name, c.Root, harness, id, append([]string{harness}, extra...))
			}
			if err := os.Chdir(c.Root); err != nil {
				return err
			}
			env := append(os.Environ(), "AW_SESSION="+harness+":"+id, fmt.Sprintf("AW_SESSION_PID=%d", os.Getpid()), "PWD="+c.Root)
			// The harness replaces this process: it keeps this PID, so the session's process identity
			// recorded at the claim stays true.
			return syscall.Exec(path, append([]string{harness}, extra...), env)
		}),
	}
	cmd.Flags().StringVar(&role, "role", "", "the team role this lane works under")
	cmd.Flags().StringVar(&harness, "harness", "", "the harness to launch (default: [user] harness)")
	cmd.Flags().StringVar(&branch, "branch", "", "start the lane on this existing branch")
	cmd.Flags().BoolVar(&useMux, "mux", false, "open it in your terminal multiplexer")
	return cmd
}

// openInMux starts a session in the multiplexer and says where it went.
func openInMux(cmd *cobra.Command, m config.Multiplexer, name, dir, harness, id string, argv []string) error {
	where, err := mux.Open(cmd.Context(), m, mux.Launch{Name: name, Dir: dir, Env: []string{"AW_SESSION=" + harness + ":" + id}, Argv: argv}, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "aw: opened %s in %s\n", harness, where)
	return nil
}

// usageError is a usage mistake found after cobra parsed the command line.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
