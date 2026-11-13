package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
)

// teamCommandNamed finds a team command, for a first argument aw itself does not know. A workspace
// whose configuration cannot be read has none: the usual usage error says what is wrong.
func teamCommandNamed(root *cobra.Command, args []string) (*config.Command, bool) {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' || slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == args[0] || c.HasAlias(args[0]) }) {
		return nil, false
	}
	if args[0] == "help" || args[0] == "completion" {
		return nil, false
	}
	_, c, err := loadConfig()
	if err != nil || c.Commands[args[0]] == nil {
		return nil, false
	}
	return c.Commands[args[0]], true
}

// runTeamCommand runs a named command in the right project and lane (R44, R51). A command with needs
// waits its turn, holds its seats while it runs and releases them when it exits.
func runTeamCommand(name string, c *config.Command, args []string, stdout, stderr io.Writer) (int, error) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	w, err := openForWrite()
	if err != nil {
		return ExitError, err
	}
	defer w.Close()
	m, b := manager(w), newBroker(w)
	h, err := holder(w, m)
	if err != nil {
		return ExitError, err
	}
	if h.PID = os.Getpid(); h.PID > 0 {
		if h.Start, err = platform.ProcessIdentity(h.PID); err != nil {
			return ExitError, err
		}
	}
	if err := awaitSetup(ctx, w, m, c.Project, c.After, stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130, nil
		}
		return ExitError, err
	}
	if len(c.Needs) > 0 {
		release, err := holdSeats(ctx, w, m, b, h, c.Needs, stderr)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return 130, nil
			}
			return ExitError, err
		}
		defer release()
	}
	where, err := m.Locate(c.Project)
	if err != nil {
		return ExitError, err
	}
	leases, err := state.Leases(w.store.DB())
	if err != nil {
		return ExitError, err
	}
	env := append(where.Env, seatEnv(w.cfg, b.Resources(), leases, h.Lane)...)
	argv := append([]string{"sh", "-c", c.Run + ` "$@"`, name}, args...)
	var d *runners.Devcontainer
	if w.cfg.Projects[c.Project] != nil && w.cfg.Projects[c.Project].Runner == "devcontainer" {
		if d, err = devcontainerOf(w, c.Project); err != nil {
			return ExitError, err
		}
	}
	code, err := runProject(ctx, d, where.Dir, env, argv)
	if err != nil {
		return ExitError, err
	}
	return code, nil
}

// holdSeats waits for the resources, makes the seats real, and returns how to give them back. A
// cancelled wait leaves the queue and returns the context's error.
func holdSeats(ctx context.Context, w *writeContext, m *lanes.Manager, b *broker.Broker, h broker.Holder, needs []string, stderr io.Writer) (func(), error) {
	var resources []broker.Resource
	for _, need := range needs {
		r, err := b.Lookup(need)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}
	release := func() {
		for _, r := range resources {
			if stopping, grace, err := b.Release(context.Background(), h.Lane, r.Name); err == nil {
				for _, p := range stopping {
					procs.Stop(p, grace)
				}
			} else if !errors.Is(err, broker.ErrNotHeld) {
				fmt.Fprintf(stderr, "aw: warning: releasing %s: %v\n", r.Name, err)
			}
		}
	}
	said := false
	for {
		out, err := b.Request(ctx, h, needs)
		if err != nil {
			return nil, err
		}
		broker.Stop(ctx, out.Evicted)
		if out.Granted {
			for i := range out.Leases {
				l := &out.Leases[i]
				r, err := b.Lookup(l.Resource)
				if err != nil {
					release()
					return nil, err
				}
				if err := provision(ctx, w, m, b, r, l, os.Stderr); err != nil {
					release()
					return nil, err
				}
			}
			return release, nil
		}
		if !said {
			said = true
			fmt.Fprintf(stderr, "aw: waiting for %v\n", needs)
		}
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
