package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// backgroundSetup is the hidden command aw new and aw attach start for a lane's setup.
const backgroundSetup = "__setup"

// startSetup starts a claimed lane's setup in the background (R5): it runs in its own session, so it
// outlives this command and the terminal. If it cannot start, the lane is incomplete, and says how to
// retry.
func startSetup(stderr io.Writer, w *writeContext, c *lanes.Claimed) {
	if !c.Setup {
		return
	}
	fail := func(err error) {
		fmt.Fprintf(stderr, "aw: warning: could not start the lane's setup in the background: %v → run aw setup %s\n", err, ui.Clean(c.Lane.Name))
		_ = w.store.Write(context.Background(), func(tx *sql.Tx) error {
			l, err := state.LaneByNumber(tx, c.Lane.Number)
			if err != nil {
				return err
			}
			l.State, l.Updated = state.LaneIncomplete, state.Now()
			return state.UpdateLane(tx, l)
		})
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	cmd := exec.Command(exe, backgroundSetup, strconv.Itoa(c.Lane.Number))
	cmd.Dir = w.ws.Owner
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fail(err)
		return
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	// The record covers the moment before the new process takes the setup for itself.
	if start, err := platform.ProcessIdentity(pid); err == nil {
		_ = w.store.Write(context.Background(), func(tx *sql.Tx) error {
			s, err := state.LaneSetup(tx, c.Lane.Number)
			if err != nil || s.PID != 0 {
				return err
			}
			return state.SetSetupRunner(tx, c.Lane.Number, pid, start)
		})
	}
}

// awaitSetup waits, in a lane, for the steps a command in the project depends on (S12). A hook runs
// as part of setup, so what it runs must not wait for setup.
func awaitSetup(ctx context.Context, w *writeContext, m *lanes.Manager, project string, after []string, stderr io.Writer) error {
	n := m.CallerLane()
	if n == 0 || os.Getenv("AW_PHASE") != "" {
		return nil
	}
	l, err := state.LaneByNumber(w.store.DB(), n)
	if err != nil {
		return nil // Locate says what is wrong with a lane with no record
	}
	return lifecycle.Await(ctx, w.store, l, lifecycle.Needed(project, after), func(step string) {
		fmt.Fprintf(stderr, "aw: waiting for %s to finish\n", step)
	})
}

// ownHolder is this process holding seats for a lane.
func ownHolder(l state.Lane) (broker.Holder, error) {
	h := broker.Holder{Lane: l.Number, Generation: l.Generation, Name: l.Name, PID: os.Getpid()}
	var err error
	h.Start, err = platform.ProcessIdentity(h.PID)
	return h, err
}

// runSetup runs what is left of a lane's setup.
func runSetup(ctx context.Context, w *writeContext, m *lanes.Manager, l state.Lane, out io.Writer) error {
	dir, err := m.StateDir(l.Number)
	if err != nil {
		return err
	}
	h, err := ownHolder(l)
	if err != nil {
		return err
	}
	b := newBroker(w)
	r := &lifecycle.Runner{Store: w.store, Cfg: w.cfg, Lane: l, StateDir: dir, Out: out,
		Fresh: len(w.cfg.Lanes.Data) > 0,
		Where: func(project string) (string, []string, error) {
			where, err := m.LocateIn(l.Number, project)
			if err != nil {
				return "", nil, err
			}
			leases, err := state.Leases(w.store.DB())
			if err != nil {
				return "", nil, err
			}
			return where.Dir, append(where.Env, seatEnv(w.cfg, b.Resources(), leases, l.Number)...), nil
		},
		Seats: func(ctx context.Context, needs []string) (func(), error) {
			return holdSeats(ctx, w, m, b, h, needs, os.Stderr)
		},
		Provision: func(ctx context.Context, data string, fromPool bool) error {
			return m.Data().Provision(ctx, m.ProviderLane(l), data, fromPool)
		},
		After: func(ctx context.Context) error { return m.Data().Discover(ctx, m.ProviderLane(l)) },
	}
	return r.Run(ctx)
}

// retrySetup is aw setup <lane>: the lane's setup, in the foreground.
func retrySetup(cmd *cobra.Command, ref string) error {
	w, err := openForWrite()
	if err != nil {
		return err
	}
	defer w.Close()
	m := manager(w)
	l, err := m.Resolve(ref)
	if err != nil {
		return err
	}
	if why := agentContext(); why != "" && m.CallerLane() != l.Number {
		return fmt.Errorf("an agent may only rerun its own lane's setup, and this looks like an agent session (%s) outside lane %s → ask the human to run it", why, ui.Clean(l.Name))
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	err = runSetup(ctx, w, m, l, cmd.ErrOrStderr())
	if errors.Is(err, lifecycle.ErrRunning) {
		return fmt.Errorf("lane %s's setup is already running → wait for it; aw shows its progress", ui.Clean(l.Name))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Lane %s is set up.\n", ui.Clean(l.Name))
	return nil
}

func newBackgroundSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:    backgroundSetup + " <lane number>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			l, err := state.LaneByNumber(w.store.DB(), n)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return runSetup(ctx, w, manager(w), l, nil)
		}),
	}
}
