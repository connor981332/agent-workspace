package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

const keepCommand = "__keep"

func devcontainerIn(ws *workspace.Workspace, c *config.Config, project string) (*runners.Devcontainer, error) {
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
	return d, nil
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
func hookInContainer(ctx context.Context, w *writeContext, project, dir string, env []string, run, log string) ([]string, error) {
	if p := w.cfg.Projects[project]; p == nil || p.Runner != "devcontainer" {
		return nil, nil
	}
	d, err := devcontainerOf(w, project)
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
// servers, setups in progress, and anything attached to the container.
func inUse(ctx context.Context, w *writeContext, d *runners.Devcontainer, id string) ([]string, error) {
	var out []string
	b := &broker.Broker{Cfg: w.cfg}
	leases, err := state.Leases(w.store.DB())
	if err != nil {
		return nil, err
	}
	for _, l := range leases {
		r, err := b.Lookup(l.Resource)
		if err != nil || !slices.ContainsFunc(r.Servers, func(s string) bool { return strings.HasPrefix(s, d.Project+".") }) {
			continue
		}
		who := "the owner"
		if l.Lane > 0 {
			who = "lane " + ui.Clean(l.LaneName)
		}
		out = append(out, fmt.Sprintf("%s holds a seat on %s", who, l.Resource))
	}
	setups, err := state.ActiveSetups(w.store.DB())
	if err != nil {
		return nil, err
	}
	for _, s := range setups {
		out = append(out, fmt.Sprintf("lane %d is setting up", s.Lane))
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
			if why := agentContext(); why != "" && !(use == "up" && delegatedHere("containers", []string{"up"})) {
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
					used, err := inUse(ctx, w, d, id)
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
