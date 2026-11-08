package cli

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// queuedError exits with ExitQueued: not a failure, but the caller does not have the seat yet.
type queuedError struct{ message string }

func (e *queuedError) Error() string { return e.message }

var envWord = regexp.MustCompile(`[^A-Z0-9]+`)

func envName(s string) string { return envWord.ReplaceAllString(strings.ToUpper(s), "_") }

func newBroker(w *writeContext) *broker.Broker { return &broker.Broker{Store: w.store, Cfg: w.cfg} }

// pairSession reports whether this process belongs to a live pair session.
func pairSession(w *writeContext) bool {
	ids, _ := issuedIdentities()
	for _, id := range ids {
		if rec, err := state.SessionByHarnessID(w.store.DB(), id.harness, id.id); err == nil && rec.Pair && rec.State == state.SessionActive && rec.Alive() {
			return true
		}
	}
	return false
}

// holder says who is asking. The owner is the human, or a pair session acting for the human (R61). An
// agent in the owner checkout with no lane is neither.
func holder(w *writeContext, m *lanes.Manager) (broker.Holder, error) {
	n := m.CallerLane()
	if n == 0 {
		if why := agentContext(); why != "" && !pairSession(w) {
			return broker.Holder{}, fmt.Errorf(`seats go to lanes, and the owner's seat to the human; this is an agent session with no lane (%s) → run bin/aw attach "<what you are working on>" and work in the lane root`, why)
		}
		return broker.Holder{Name: "owner"}, nil
	}
	l, err := state.LaneByNumber(w.store.DB(), n)
	if err != nil {
		return broker.Holder{}, fmt.Errorf("this lane has no record in the state (%v) → run aw doctor", err)
	}
	return broker.Holder{Lane: n, Generation: l.Generation, Name: l.Name}, nil
}

func port(c *config.Config, s *config.Server, seat int) int {
	return s.Port + c.Lanes.PortStride*seat + c.Ports.Offset
}

func serverOf(c *config.Config, full string) (project, name string, s *config.Server) {
	project, name, _ = strings.Cut(full, ".")
	if p := c.Projects[project]; p != nil {
		s = p.Servers[name]
	}
	return project, name, s
}

// startOrder puts each server after the servers it waits for.
func startOrder(c *config.Config, servers []string) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(full string) {
		if seen[full] {
			return
		}
		seen[full] = true
		project, _, s := serverOf(c, full)
		if s != nil {
			for _, a := range s.After {
				if !strings.Contains(a, ".") {
					a = project + "." + a
				}
				if strings.Contains(strings.Join(servers, " "), a) {
					visit(a)
				}
			}
		}
		out = append(out, full)
	}
	for _, s := range servers {
		visit(s)
	}
	return out
}

// served is what a seat gives its holder, for output.
type served struct {
	Resource string            `json:"resource"`
	Seat     int               `json:"seat"`
	Token    string            `json:"token,omitempty"`
	URLs     map[string]string `json:"urls,omitempty"`
}

func hostname(w *writeContext, m *lanes.Manager, lane int) string {
	if lane == 0 {
		return "localhost"
	}
	return strings.NewReplacer("{lane}", lanes.Dir(lane, w.cfg.Lanes.Max), "{namespace}", w.cfg.Workspace.Namespace).Replace(w.cfg.Lanes.Hostname)
}

func urlsOf(w *writeContext, m *lanes.Manager, r broker.Resource, l state.Lease) map[string]string {
	urls := map[string]string{}
	for _, full := range r.Servers {
		if _, name, s := serverOf(w.cfg, full); s != nil {
			urls[name] = fmt.Sprintf("http://%s:%d", hostname(w, m, l.Lane), port(w.cfg, s, l.Seat))
		}
	}
	return urls
}

// startServers starts a process resource's servers for a lease. If one fails, those already started
// are stopped, and the caller gives the seat back.
func startServers(ctx context.Context, w *writeContext, m *lanes.Manager, r broker.Resource, l state.Lease) ([]state.LeaseProc, error) {
	seatEnv := []string{fmt.Sprintf("AW_SEAT_%s=%d", envName(r.Name), l.Seat)}
	for _, full := range r.Servers {
		if _, name, s := serverOf(w.cfg, full); s != nil {
			seatEnv = append(seatEnv, fmt.Sprintf("AW_PORT_%s=%d", envName(name), port(w.cfg, s, l.Seat)))
		}
	}
	logs := filepath.Join(filepath.Dir(w.store.Path()), "owner", "logs")
	if l.Lane > 0 {
		dir, err := m.StateDir(l.Lane)
		if err != nil {
			return nil, err
		}
		logs = filepath.Join(dir, "logs")
	}
	var started []state.LeaseProc
	undo := func() {
		for _, p := range started {
			procs.Stop(procs.Proc{PID: p.PID, Start: p.Start}, 2*time.Second)
		}
	}
	for _, full := range startOrder(w.cfg, r.Servers) {
		project, name, s := serverOf(w.cfg, full)
		if s == nil {
			undo()
			return nil, fmt.Errorf("server %s is not configured → check workspace.toml", full)
		}
		where, err := m.Locate(project)
		if err != nil {
			undo()
			return nil, err
		}
		p := port(w.cfg, s, l.Seat)
		log := filepath.Join(logs, project+"."+name+".log")
		proc, err := procs.Start(ctx, procs.Spec{Command: strings.ReplaceAll(s.Command, "{port}", strconv.Itoa(p)),
			Dir: where.Dir, Env: append(where.Env, seatEnv...), Log: log, Ready: strings.ReplaceAll(s.Ready, "{port}", strconv.Itoa(p))})
		if err != nil {
			undo()
			return nil, fmt.Errorf("server %s did not start: %w", full, err)
		}
		started = append(started, state.LeaseProc{Server: full, PID: proc.PID, Start: proc.Start, Log: log})
	}
	return started, nil
}

// stopEvicted signals every evicted group first, so that the owner waits only for the grace period.
func stopEvicted(evicted []broker.Evicted, errOut interface{ Write([]byte) (int, error) }) {
	for _, e := range evicted {
		fmt.Fprintf(errOut, "aw: stopping %s, held by lane %s\n", e.Resource, ui.Clean(e.Lane))
		for _, p := range e.Procs {
			procs.Signal(p)
		}
	}
	for _, e := range evicted {
		for _, p := range e.Procs {
			procs.Finish(p, e.Grace)
		}
	}
}

func newServeCmd() *cobra.Command {
	var wait, asJSON bool
	cmd := &cobra.Command{
		Use:   "serve <resource> [--wait]",
		Short: "Take a seat on a resource, and start its servers",
		Long: `Take a seat on a resource. For a process resource, aw starts its servers for this lane on the seat's
ports. For a token resource, it hands out a token. If every seat is taken, this joins the queue,
says its position and exits with status 3; you are told on a later tool call when a seat is yours.
With --wait it waits in the foreground instead. The owner goes to the front of the queue.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m, b := manager(w), newBroker(w)
			r, err := b.Lookup(args[0])
			if err != nil {
				return err
			}
			h, err := holder(w, m)
			if err != nil {
				return err
			}
			if r.Kind == "token" && h.Lane > 0 {
				if ids, _ := issuedIdentities(); len(ids) == 1 {
					if rec, err := state.SessionByHarnessID(w.store.DB(), ids[0].harness, ids[0].id); err == nil {
						h.PID, h.Start = rec.PID, rec.PIDStart
					}
				}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			out, err := b.Request(ctx, h, []string{r.Name})
			if err != nil {
				return err
			}
			for wait && !out.Granted {
				select {
				case <-ctx.Done():
					// Leaving the line is the human's choice: the place must not stay behind.
					if _, _, err := b.Release(context.Background(), h.Lane, r.Name); err != nil && !errors.Is(err, broker.ErrNotHeld) {
						return err
					}
					return errors.New("stopped waiting, and left the queue")
				case <-time.After(100 * time.Millisecond):
				}
				if out, err = b.Request(ctx, h, []string{r.Name}); err != nil {
					return err
				}
			}
			stopEvicted(out.Evicted, cmd.ErrOrStderr())
			if !out.Granted {
				msg := fmt.Sprintf("position %d in line for %s → you will be told when a seat is yours; or wait with bin/aw serve %s --wait", out.Positions[r.Name], r.Name, r.Name)
				if asJSON {
					_ = writeJSON(cmd.OutOrStdout(), map[string]any{"queued": true, "resource": r.Name, "position": out.Positions[r.Name]})
				}
				return &queuedError{msg}
			}
			l := out.Leases[0]
			res := served{Resource: r.Name, Seat: l.Seat, Token: l.Token}
			if r.Kind == "process" {
				res.URLs = urlsOf(w, m, r, l)
				running, err := state.LeaseProcs(w.store.DB(), l.ID)
				if err != nil {
					return err
				}
				if len(running) == 0 {
					started, err := startServers(ctx, w, m, r, l)
					if err != nil {
						if _, _, rerr := b.Release(context.Background(), h.Lane, r.Name); rerr != nil && !errors.Is(rerr, broker.ErrNotHeld) {
							return fmt.Errorf("%w; giving the seat back also failed (%v)", err, rerr)
						}
						return err
					}
					if err := b.RecordProcs(ctx, l.ID, started); err != nil {
						return err
					}
				}
			}
			stdout := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(stdout, res)
			}
			fmt.Fprintf(stdout, "Holding seat %d on %s.\n", l.Seat, r.Name)
			if res.Token != "" {
				fmt.Fprintf(stdout, "  token %s (AW_TOKEN_%s)\n", ui.Clean(res.Token), envName(r.Name))
			}
			for _, full := range r.Servers {
				if _, name, _ := serverOf(w.cfg, full); res.URLs[name] != "" {
					fmt.Fprintf(stdout, "  %s %s\n", name, res.URLs[name])
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait in the foreground for a seat")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <resource>",
		Short: "Give up a seat, or a place in line, and stop this lane's servers on it",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m, b := manager(w), newBroker(w)
			r, err := b.Lookup(args[0])
			if err != nil {
				return err
			}
			h, err := holder(w, m)
			if err != nil {
				return err
			}
			stop, grace, err := b.Release(cmd.Context(), h.Lane, r.Name)
			if errors.Is(err, broker.ErrNotHeld) {
				fmt.Fprintf(cmd.OutOrStdout(), "This lane holds no seat on %s.\n", r.Name)
				return nil
			}
			if err != nil {
				return err
			}
			for _, p := range stop {
				procs.Stop(p, grace)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Released %s.\n", r.Name)
			return nil
		}),
	}
}

func newQueueCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Show who holds and who waits for each resource",
		Long:  "Show who holds and who waits for each resource. It only reads: a dead holder is shown, and left for the next command that changes something to recover.",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, cfg, err := loadConfig()
			if err != nil {
				return err
			}
			dir, err := ws.StateDir()
			if err != nil {
				return err
			}
			type row struct {
				Resource string             `json:"resource"`
				Capacity int                `json:"capacity"`
				Holders  []state.Lease      `json:"holders"`
				Waiting  []state.QueueEntry `json:"waiting"`
			}
			var rows []row
			b := &broker.Broker{Cfg: cfg}
			var leases []state.Lease
			var queue []state.QueueEntry
			store, err := state.Open(dir, state.ReadOnly)
			switch {
			case errors.Is(err, state.ErrNoState):
			case err != nil:
				return err
			default:
				defer store.Close()
				if leases, err = state.Leases(store.DB()); err != nil {
					return err
				}
				if queue, err = state.QueueEntries(store.DB()); err != nil {
					return err
				}
			}
			for _, r := range b.Resources() {
				one := row{Resource: r.Name, Capacity: r.Capacity, Holders: []state.Lease{}, Waiting: []state.QueueEntry{}}
				for _, l := range leases {
					if l.Resource == r.Name {
						one.Holders = append(one.Holders, l)
					}
				}
				for _, e := range queue {
					if e.Resource == r.Name {
						one.Waiting = append(one.Waiting, e)
					}
				}
				rows = append(rows, one)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if rows == nil {
					rows = []row{}
				}
				return writeJSON(out, map[string]any{"resources": rows})
			}
			who := func(lane int, name string) string {
				if lane == 0 {
					return "owner"
				}
				return ui.Clean(name)
			}
			for _, r := range rows {
				fmt.Fprintf(out, "%s (%d of %d)\n", r.Resource, len(r.Holders), r.Capacity)
				for _, l := range r.Holders {
					fmt.Fprintf(out, "  seat %d  %-20s %s\n", l.Seat, who(l.Lane, l.LaneName), l.State)
				}
				for i, e := range r.Waiting {
					fmt.Fprintf(out, "  %d. waiting  %s\n", i+1, who(e.Lane, e.LaneName))
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the queue as JSON")
	return cmd
}
