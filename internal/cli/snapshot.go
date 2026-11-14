package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// quiesceGrace is how long a lane's server has to stop before a snapshot or restore.
const quiesceGrace = 10 * time.Second

// inLane is the lane this command runs in, for snapshots: a lane's data is what they copy.
func inLane(w *writeContext, m *lanes.Manager, verb string) (state.Lane, error) {
	n := m.CallerLane()
	if n == 0 {
		return state.Lane{}, fmt.Errorf("aw %s acts on a lane's data → run it in the lane's directory", verb)
	}
	return state.LaneByNumber(w.store.DB(), n)
}

// quiesce stops the lane's servers (S19), because Postgres cannot copy a database in use, and returns
// how to start them again.
func quiesce(ctx context.Context, w *writeContext, m *lanes.Manager, lane int) (func() error, error) {
	b := newBroker(w)
	leases, err := state.Leases(w.store.DB())
	if err != nil {
		return nil, err
	}
	var stopped []state.Lease
	for _, l := range leases {
		if l.Lane != lane || l.State != state.LeaseHeld {
			continue
		}
		running, err := state.LeaseProcs(w.store.DB(), l.ID)
		if err != nil {
			return nil, err
		}
		if len(running) == 0 {
			continue
		}
		for _, p := range running {
			procs.Stop(procs.Proc{PID: p.PID, Start: p.Start, Log: p.Log}, quiesceGrace)
		}
		if err := b.RecordProcs(ctx, l.ID, nil); err != nil {
			return nil, err
		}
		stopped = append(stopped, l)
	}
	return func() error {
		var failed []string
		for i := range stopped {
			l := &stopped[i]
			r, err := b.Lookup(l.Resource)
			if err == nil {
				err = provision(context.WithoutCancel(ctx), w, m, b, r, l, nil)
			}
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s (%v)", l.Resource, err))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("the lane's servers on %s did not start again → run aw stop, then aw serve", strings.Join(failed, ", "))
		}
		return nil
	}, nil
}

// runRestoreHooks runs each project's restore hook (S19), which can migrate data behind the code.
func runRestoreHooks(ctx context.Context, w *writeContext, m *lanes.Manager, b *broker.Broker, l state.Lane) error {
	projects := make([]string, 0, len(w.cfg.Projects))
	for name := range w.cfg.Projects {
		projects = append(projects, name)
	}
	slices.Sort(projects)
	dir, err := m.StateDir(l.Number)
	if err != nil {
		return err
	}
	h, err := ownHolder(l)
	if err != nil {
		return err
	}
	for _, project := range projects {
		hook := w.cfg.Projects[project].Lifecycle.Restore
		if hook == nil {
			continue
		}
		release := func() {}
		if len(hook.Needs) > 0 {
			if release, err = holdSeats(ctx, w, m, b, h, hook.Needs, os.Stderr); err != nil {
				return err
			}
		}
		where, err := m.LocateIn(l.Number, project)
		if err != nil {
			release()
			return err
		}
		log := filepath.Join(dir, "logs", "setup", project+".restore.log")
		argv, err := hookInContainer(ctx, w, setupLane{l.Number, l.Generation}, project, where.Dir, append(append([]string{}, where.Env...), lifecycle.HookEnv(lifecycle.PhaseRestore, true)...), hook.Run, log)
		if err != nil {
			release()
			return err
		}
		_, err = lifecycle.Exec(ctx, lifecycle.Command{Dir: where.Dir, Env: where.Env, Run: hook.Run, Argv: argv, Timeout: time.Duration(hook.Timeout), Log: log, Phase: lifecycle.PhaseRestore, Fresh: true})
		release()
		if err != nil {
			return fmt.Errorf("the data was restored, but %s's restore hook failed: %v (log %s) → fix it and run the restore again", project, err, log)
		}
	}
	return nil
}

func newSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot <name>",
		Short: "Copy this lane's data under a name that every lane can restore",
		Long: `Copy this lane's data under a name. Names are shared across the workspace, so that one onboarded
snapshot can seed other lanes, and a name in use is never overwritten. The lane's servers stop while
the copy is made, and start again afterwards.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := inLane(w, m, "snapshot")
			if err != nil {
				return err
			}
			if err := awaitSetup(cmd.Context(), w, m, "", nil, cmd.ErrOrStderr()); err != nil {
				return err
			}
			resume, err := quiesce(cmd.Context(), w, m, l.Number)
			if err != nil {
				return err
			}
			taken, err := m.Data().Snapshot(cmd.Context(), m.ProviderLane(l), args[0])
			if rerr := resume(); rerr != nil && err == nil {
				err = rerr
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Took snapshot %s of %s.\n", ui.Clean(args[0]), strings.Join(taken, ", "))
			return nil
		}),
	}
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <name>",
		Short: "Replace this lane's data with a named snapshot",
		Long: `Replace this lane's data with a named snapshot, then run each project's restore hook, which can
migrate data behind the code. The lane's servers stop first and start again afterwards.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := inLane(w, m, "restore")
			if err != nil {
				return err
			}
			if err := awaitSetup(cmd.Context(), w, m, "", nil, cmd.ErrOrStderr()); err != nil {
				return err
			}
			resume, err := quiesce(cmd.Context(), w, m, l.Number)
			if err != nil {
				return err
			}
			restored, err := m.Data().Restore(cmd.Context(), m.ProviderLane(l), args[0])
			if err == nil {
				err = runRestoreHooks(cmd.Context(), w, m, newBroker(w), l)
			}
			if rerr := resume(); rerr != nil && err == nil {
				err = rerr
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored %s from snapshot %s.\n", strings.Join(restored, ", "), ui.Clean(args[0]))
			return nil
		}),
	}
}

func newSnapshotsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "snapshots",
		Short: "List the workspace's named snapshots",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, _, err := loadConfig()
			if err != nil {
				return err
			}
			dir, err := ws.StateDir()
			if err != nil {
				return err
			}
			store, err := state.Open(dir, state.ReadOnly)
			if errors.Is(err, state.ErrNoState) {
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), []state.Snapshot{})
				}
				return nil
			}
			if err != nil {
				return err
			}
			defer store.Close()
			list, err := state.Snapshots(store.DB())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), list)
			}
			for _, s := range list {
				fmt.Fprintf(cmd.OutOrStdout(), "%-24s %-10s taken %s\n", ui.Clean(s.Name), ui.Clean(s.Data), s.Created)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the snapshots as JSON")
	return cmd
}
