package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

const keepCommand = "__keep"

func devcontainerIn(ws *workspace.Workspace, c *config.Config, project string) (*runners.Devcontainer, error) {
	return devcontainerFor(ws, c, project, setupLane{})
}

// setupLane is the lane setup asking for a container, or the zero value for anything else. Its own
// setup is not a use of the container, and its entering the container is recorded for the others.
type setupLane struct{ number, generation int }

func devcontainerFor(ws *workspace.Workspace, c *config.Config, project string, req setupLane) (*runners.Devcontainer, error) {
	if p := c.Projects[project]; p == nil || p.Runner != "devcontainer" {
		return nil, fmt.Errorf("project %q does not run in a devcontainer → set [projects.%s] runner = \"devcontainer\"", project, project)
	}
	dir, err := ws.StateDir()
	if err != nil {
		return nil, err
	}
	d, err := runners.NewDevcontainer(c, ws.Owner, project, dir)
	if err != nil {
		return nil, err
	}
	// Whichever project starts first brings up what the projects share (F7).
	d.BeforeStart = infraOf(ws, c).Up
	d.InUse = func(ctx context.Context, id string) ([]string, error) {
		store, err := state.Open(dir, state.ReadOnly)
		if errors.Is(err, state.ErrNoState) {
			if id == "" {
				return nil, nil
			}
			return d.Sessions(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		defer store.Close()
		return inUse(ctx, c, store.DB(), d, id, dir, req.number)
	}
	if req.number > 0 {
		d.Entered = func() error { return recordEntered(dir, c, req, project) }
	}
	return d, nil
}

// enteredPath is where a lane's setup records that it has used a project's container.
func enteredPath(stateDir string, c *config.Config, lane int, project string) string {
	return filepath.Join(stateDir, "lanes", lanes.Dir(lane, c.Lanes.Max), "containers", project)
}

// recordEntered notes, for the setup's generation, that the lane's setup has used the container.
func recordEntered(stateDir string, c *config.Config, req setupLane, project string) error {
	path := enteredPath(stateDir, c, req.number, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(req.generation)+"\n"), 0o600)
}

// entered says whether a setup in progress has used the project's container. What cannot be read
// counts as a use, since the container is then not proven idle.
func entered(stateDir string, c *config.Config, s state.Setup, project string) bool {
	data, err := os.ReadFile(enteredPath(stateDir, c, s.Lane, project))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	gen, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err != nil || gen == s.Generation
}

func devcontainerOf(w *writeContext, project string) (*runners.Devcontainer, error) {
	return devcontainerIn(w.ws, w.cfg, project)
}

// setEnv puts values in this process's environment, for the docker client to read by name.
func setEnv(env []string) {
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			os.Setenv(k, v)
		}
	}
}

// runProject runs a command in a project: on the host when d is nil, otherwise in its container, on
// a terminal when there is one (a terminal carries signals itself), else through a keeper.
func runProject(ctx context.Context, d *runners.Devcontainer, dir string, env, argv []string) (int, error) {
	if d == nil {
		return runners.RunHost(dir, env, argv)
	}
	id, err := d.Up(ctx, os.Stderr)
	if err != nil {
		return 1, err
	}
	if dir, err = d.Within(dir); err != nil {
		return 1, err
	}
	if runners.Interactive() {
		return runners.RunHost(dir, env, append(append([]string{"docker"}, d.ExecArgs(id, dir, env, "-it")...), argv...))
	}
	setEnv(env)
	return procs.Keep(procs.KeepSpec{Exec: d.ExecArgs(id, dir, env, "-i"), Container: id, User: d.User, Argv: argv}, os.Stdin, os.Stdout, os.Stderr)
}

// keeperArgv is the host command that runs argv in a running container under a keeper.
func keeperArgv(d *runners.Devcontainer, id, dir string, env, argv []string, log string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	spec, err := json.Marshal(procs.KeepSpec{Exec: d.ExecArgs(id, dir, env, "-i"), Container: id, User: d.User, Argv: argv, Record: procs.RecordPath(log)})
	if err != nil {
		return nil, err
	}
	return []string{exe, keepCommand, string(spec)}, nil
}

// hookInContainer returns the command that runs a hook in its project's container, or nil for a
// project on the host. What starting the container says goes to the hook's log.
// lane is the lane whose setup runs the hook, or the zero value for the owner.
func hookInContainer(ctx context.Context, w *writeContext, lane setupLane, project, dir string, env []string, run, log string) ([]string, error) {
	if p := w.cfg.Projects[project]; p == nil || p.Runner != "devcontainer" {
		return nil, nil
	}
	d, err := devcontainerFor(w.ws, w.cfg, project, lane)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	id, err := d.Up(ctx, f)
	if err != nil {
		return nil, err
	}
	if dir, err = d.Within(dir); err != nil {
		return nil, err
	}
	return keeperArgv(d, id, dir, env, []string{"sh", "-c", run}, log)
}

// intoContainer turns a server's start into a keeper in its project's container, and its readiness
// probe into a docker exec.
func intoContainer(ctx context.Context, w *writeContext, project string, s *procs.Spec) error {
	d, err := devcontainerOf(w, project)
	if err != nil {
		return err
	}
	id, err := d.Up(ctx, os.Stderr)
	if err != nil {
		return err
	}
	if s.Dir, err = d.Within(s.Dir); err != nil {
		return err
	}
	if s.Argv, err = keeperArgv(d, id, s.Dir, s.Env, []string{"sh", "-c", s.Command}, s.Log); err != nil {
		return err
	}
	if s.Ready != "" {
		s.ReadyArgv = append(append([]string{"docker"}, d.ExecArgs(id, s.Dir, s.Env, "-i")...), "sh", "-c", s.Ready)
	}
	return nil
}

func newKeepCmd() *cobra.Command {
	return &cobra.Command{
		Use:    keepCommand + " <spec>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			var spec procs.KeepSpec
			if err := json.Unmarshal([]byte(args[0]), &spec); err != nil {
				return err
			}
			code, err := procs.Keep(spec, nil, os.Stdout, os.Stderr)
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitStatus{code}
			}
			return nil
		}),
	}
}

// inUse says what is using a project's container (R49): lanes or the owner holding seats on its
// servers, other lanes' setups in progress that have used it, and anything attached to it. The
// setup of lane self (0 for none) is the caller, and does not count.
func inUse(ctx context.Context, cfg *config.Config, q state.Querier, d *runners.Devcontainer, id, stateDir string, self int) ([]string, error) {
	var out []string
	b := &broker.Broker{Cfg: cfg}
	leases, err := state.Leases(q)
	if err != nil {
		return nil, err
	}
	for _, l := range leases {
		r, err := b.Lookup(l.Resource)
		if err != nil {
			return nil, fmt.Errorf("the configuration no longer defines leased resource %s → restore its configuration, stop its holder with aw stop %s, then retry", ui.Clean(l.Resource), ui.Clean(l.Resource))
		}
		if !slices.ContainsFunc(r.Servers, func(s string) bool { return strings.HasPrefix(s, d.Project+".") }) {
			continue
		}
		who := "the owner"
		if l.Lane > 0 {
			who = "lane " + ui.Clean(l.LaneName)
		}
		out = append(out, fmt.Sprintf("%s holds a seat on %s", who, l.Resource))
	}
	setups, err := state.ActiveSetups(q)
	if err != nil {
		return nil, err
	}
	for _, s := range setups {
		if s.Lane == self {
			continue
		}
		if entered(stateDir, cfg, s, d.Project) {
			out = append(out, fmt.Sprintf("lane %d is setting up in it", s.Lane))
		}
	}
	if id != "" {
		sessions, err := d.Sessions(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			out = append(out, "attached: "+ui.Clean(s))
		}
	}
	return out, nil
}

func containerProjects(c *config.Config, args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	var out []string
	for name, p := range c.Projects {
		if p.Runner == "devcontainer" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no project runs in a devcontainer → set a project's runner = \"devcontainer\"")
	}
	return out, nil
}

// containersVerb is one of the human's container commands: up, recreate or down. Removal refuses
// while the container is in use; there is deliberately no override (R49).
func containersVerb(use, short string, remove, start bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " [project…]",
		Short: short,
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if why := unguardedAgent(); why != "" && !(use == "up" && delegatedHere("containers", []string{"up"})) {
				return &commandError{fmt.Errorf("aw containers %s is only for the human, and this looks like an agent session (%s) → ask the human to run it in their own terminal", use, why)}
			}
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			projects, err := containerProjects(w.cfg, args)
			if err != nil {
				return err
			}
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			for _, project := range projects {
				d, err := devcontainerOf(w, project)
				if err != nil {
					return err
				}
				if remove {
					id, err := d.Running(ctx)
					if err != nil {
						return err
					}
					stateDir, err := w.ws.StateDir()
					if err != nil {
						return err
					}
					used, err := inUse(ctx, w.cfg, w.store.DB(), d, id, stateDir, 0)
					if err != nil {
						return err
					}
					if len(used) > 0 {
						return fmt.Errorf("the %s container is in use, so aw will not remove it: %s → stop these first, then run this again", project, strings.Join(used, "; "))
					}
					if err := d.Remove(ctx); err != nil {
						return err
					}
				}
				if start {
					if _, err := d.Up(ctx, cmd.ErrOrStderr()); err != nil {
						return err
					}
					fmt.Fprintf(out, "The %s container is running.\n", project)
				} else {
					fmt.Fprintf(out, "The %s container is stopped and removed. Its volumes are kept.\n", project)
				}
			}
			return nil
		}),
	}
}

func newContainersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "containers", Short: "Start, recreate or remove the projects' devcontainers (the human's commands)"}
	claim := &cobra.Command{
		Use:   "claim <project>",
		Short: "Remove the project's container if it is left over from a moved checkout. For initializeCommand",
		Long: `Remove a container left over from a moved checkout, or refuse if that other checkout is live and
running, and write the project's Compose fragment. Each project's devcontainer.json runs this as its
initializeCommand, so that VS Code and aw treat a moved checkout the same way.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			d, err := devcontainerIn(ws, c, args[0])
			if err != nil {
				return err
			}
			return d.Claim(cmd.Context(), cmd.ErrOrStderr())
		}),
	}
	cmd.AddCommand(claim,
		containersVerb("up", "Start the projects' containers, and run nothing in them", false, true),
		containersVerb("recreate", "Replace the projects' containers, refusing while one is in use", true, true),
		containersVerb("down", "Stop and remove the projects' containers, refusing while one is in use", true, false))
	return cmd
}
