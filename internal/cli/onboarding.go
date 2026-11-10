package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	gitrun "github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/harness/launch"
	"github.com/connor981332/agent-workspace/internal/inspect"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

func scanProjects(ws *workspace.Workspace, c *config.Config) ([]*inspect.Project, error) {
	var out []*inspect.Project
	for _, name := range slices.Sorted(mapsKeys(c.Projects)) {
		dir := filepath.Join(ws.Owner, c.Projects[name].Directory)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		p, err := inspect.Scan(name, dir, c.Projects[name].Directory)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func newInspectCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Report what lanes will need before anything is created: checkout hooks, local files, tools. It only reads",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			projects, err := scanProjects(ws, c)
			if err != nil {
				return err
			}
			inspect.Preflight(cmd.Context(), ws.Owner, c, projects)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"projects": projects, "other_hooks": sandbox.OtherHooks(ws.Owner)})
			}
			for _, h := range sandbox.OtherHooks(ws.Owner) {
				fmt.Fprintf(cmd.OutOrStdout(), "! a hook that is not aw's, which also runs in lanes: %s\n", ui.Clean(h))
			}
			for _, p := range projects {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): %s; %d environment variables, %d findings\n", p.Name, p.Directory, strings.Join(p.Stacks, ", "), len(p.EnvVars), len(p.Findings))
				for _, line := range p.PreflightLines() {
					fmt.Fprintf(cmd.OutOrStdout(), "  ! %s\n", ui.Clean(line))
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the facts as JSON")
	return cmd
}

func doctorProjects(cmd *cobra.Command, asJSON bool) error {
	ws, c, err := loadConfig()
	if err != nil {
		return err
	}
	projects, err := scanProjects(ws, c)
	if err != nil {
		return err
	}
	findings := []inspect.Finding{}
	for _, p := range projects {
		findings = append(findings, p.Findings...)
	}
	if asJSON {
		if err := writeJSON(cmd.OutOrStdout(), map[string]any{"findings": findings}); err != nil {
			return err
		}
	} else {
		for _, f := range findings {
			fmt.Fprintf(cmd.OutOrStdout(), "! %s %s:%d [%s]: %s → %s\n", f.Project, f.File, f.Line, f.Kind, ui.Clean(f.Message), ui.Clean(f.Fix))
		}
		if len(findings) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No project has anything that would collide in lanes, that a program can see.")
		}
	}
	if len(findings) > 0 {
		return &silentError{fmt.Errorf("%d findings", len(findings))}
	}
	return nil
}

func newOnboardCmd() *cobra.Command {
	var review, keepOtherHooks bool
	var harness string
	cmd := &cobra.Command{
		Use:   "onboard [--review] [--harness h] [--keep-other-hooks]",
		Short: "Set this workspace up, and have an agent design its configuration for you to review",
		Long: `The one command to start with. It runs everything aw setup does, reports what aw inspect finds, and
then starts a harness with the workspace-design skill. If setup or creating the lane fails, the agent
still starts, in the owner checkout with the failure in front of it, to fix it, skip it or ask you.

Nothing needs to be committed first. The agent works in a lane when the workspace repository has a
commit to start from, and its result stays on the lane's branch for review. In a workspace kept as
plain files it works in the owner checkout as a pair session.

Hooks that are not aw's are turned off in the onboarding lane, because another system's guard can
refuse everything in a lane, and the agent is told which ones. Your checkout keeps them.

Hooks see your environment, so VARIABLE=value aw onboard reaches them. To keep a setting, put it in
workspace.toml under [projects.<name>.env].

With --review it compares the current configuration with the repositories, and proposes changes only
for what is new or has drifted.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			errOut := cmd.ErrOrStderr()
			fmt.Fprintln(errOut, "aw onboard: setting up this workspace, then starting an agent. On a large project this can take a few minutes, and progress is shown here.")
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			var output bytes.Buffer
			cmd.SetErr(io.MultiWriter(errOut, &output))
			setupErr := setupWorkspace(cmd, ws, c, harness, false, false)
			cmd.SetErr(errOut)
			if setupErr != nil {
				fmt.Fprintf(errOut, "aw onboard: setup did not finish (%v). The agent will start with this in front of it.\n", setupErr)
			}
			if fresh, err := config.Load(ws.Owner); err == nil {
				c = fresh
			}
			if harness == "" {
				harness = c.User.Harness
			}
			fmt.Fprintln(errOut, "aw onboard: checking the projects for what lanes will need")
			preflight := reportPreflight(cmd, ws, c, errOut)

			prompt := "Use the aw-workspace-design skill to design this workspace, then stop and report."
			purpose := lanes.PurposeOnboard
			if review {
				prompt = "Use the aw-workspace-design skill, but only for what is new or has drifted: compare workspace.toml with aw inspect --json, and propose changes for a new project, a new server or a new dependency on data. Leave the rest. Then stop and report."
				purpose = lanes.PurposeReview
			}
			if preflight != "" {
				prompt += "\n\nWhat aw inspect found before anything was created:\n" + preflight
			}
			if setupErr != nil {
				prompt += failureContext(setupErr, output.String())
			}
			if err := os.Chdir(ws.Owner); err != nil {
				return err
			}
			laneNote, ownerNote := "", ""
			if others := sandbox.OtherHooks(ws.Owner); len(others) > 0 {
				list := strings.Join(others, "\n")
				ownerNote = "\n\nThese hooks are not aw's, and they run in this checkout. If one refuses your tool calls, say so and ask the human to remove it:\n" + list
				if keepOtherHooks {
					fmt.Fprintf(errOut, "aw onboard: keeping hooks that are not aw's, as asked. They run in the lane too:\n  %s\n", strings.Join(others, "\n  "))
					laneNote = "\n\nThese hooks are not aw's, and they run in this lane too, because the human kept them:\n" + list
				} else {
					fmt.Fprintf(errOut, "aw onboard: turning off hooks that are not aw's, in the onboarding lane only, because another system's guard can refuse everything in a lane. Your checkout keeps them, and --keep-other-hooks leaves them on:\n  %s\n", strings.Join(others, "\n  "))
					laneNote = "\n\nThese hooks are not aw's, and aw turned them off in this lane only. Decide whether the workspace should keep each one, and change the harness files on your branch to match:\n" + list
				}
			}
			m := &lanes.Manager{WS: ws, Cfg: c}
			var failed error
			if !m.CanReview(cmd.Context()) {
				prompt += ownerNote + "\n\nThis workspace is not in a Git repository with a commit, so there is no lane branch to review: you are a pair session in the owner checkout. Leave your changes there, and report them."
			} else {
				failed = startLaneSession(cmd, purpose, harness, "", "", false, !keepOtherHooks, launch.PromptArgs(harness, prompt+laneNote))
				if failed == nil {
					return nil
				}
				fmt.Fprintf(errOut, "aw onboard: no lane could be created (%v). Starting the agent in the owner checkout to work on that.\n", failed)
				prompt += ownerNote + failureContext(failed, "") + "\n\nNo lane could be created, so you are a pair session in the owner checkout. Fix what stops lanes first, then carry on."
			}
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			if failed != nil || setupErr != nil {
				offerDelegation(cmd, w)
			}
			return startPairSession(cmd, w, harness, false, launch.PromptArgs(harness, prompt))
		}),
	}
	cmd.Flags().BoolVar(&review, "review", false, "propose changes only for what is new or has drifted")
	cmd.Flags().StringVar(&harness, "harness", "", "the harness to launch")
	cmd.Flags().BoolVar(&keepOtherHooks, "keep-other-hooks", false, "leave hooks that are not aw's on in the onboarding lane")
	return cmd
}

const maxFailureOutput = 4 << 10

// failureContext hands the agent a failure, so nobody has to copy it across by hand.
func failureContext(err error, output string) string {
	text := "\n\nSomething failed before you started, in aw's deterministic setup:\n" + ui.Clean(err.Error())
	if output = strings.TrimSpace(output); output != "" {
		if len(output) > maxFailureOutput {
			output = "…" + output[len(output)-maxFailureOutput:]
		}
		text += "\n\nWhat aw printed:\n" + output
	}
	return text + "\n\nLook into this yourself first: read the logs it names, and use bin/aw doctor and bin/aw inspect --json. Decide for each problem whether to fix it, skip it or ask the human, and do not ask the human to copy output or commands for you."
}

// offerDelegation lets the agent rerun aw setup while it works on a failure, if the human says so.
func offerDelegation(cmd *cobra.Command, w *writeContext) {
	if !confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), "Let the agent rerun aw setup for the next hour while it works on this?") {
		return
	}
	if err := w.store.Write(cmd.Context(), func(tx *sql.Tx) error { _, err := state.Grant(tx, time.Hour); return err }); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "aw: warning: the delegation was not granted: %v\n", err)
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "aw: delegated maintenance for one hour. aw delegate --revoke ends it.")
}

// reportPreflight prints what lanes will need and do not have, and returns it for the agent.
func reportPreflight(cmd *cobra.Command, ws *workspace.Workspace, c *config.Config, out io.Writer) string {
	projects, err := scanProjects(ws, c)
	if err != nil {
		fmt.Fprintf(out, "aw: warning: the projects could not be inspected: %v\n", err)
		return ""
	}
	inspect.Preflight(cmd.Context(), ws.Owner, c, projects)
	var b strings.Builder
	for _, p := range projects {
		for _, line := range p.PreflightLines() {
			fmt.Fprintf(&b, "%s: %s\n", p.Name, line)
		}
	}
	text := strings.TrimRight(b.String(), "\n")
	if text != "" {
		fmt.Fprintln(out, text)
	}
	return text
}

func newAddProjectCmd() *cobra.Command {
	var directory string
	var noClone bool
	cmd := &cobra.Command{
		Use:   "project <alias> [repo-url]",
		Short: "Add a project repository to this workspace",
		Args:  cobra.RangeArgs(1, 2),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if why := agentContext(); why != "" {
				return &commandError{fmt.Errorf("aw add is only for the human, and this looks like an agent session (%s) → ask the human to run it in their own terminal", why)}
			}
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			if ws.LaneRoot != "" {
				return fmt.Errorf("projects are added in the owner checkout → run this in %s", ui.Clean(ws.Owner))
			}
			alias := args[0]
			if _, exists := c.Projects[alias]; exists {
				return fmt.Errorf("the workspace already has a project named %s", alias)
			}
			dir := directory
			if dir == "" {
				dir = alias
			}
			url := ""
			if len(args) == 2 {
				url = args[1]
			}
			if !noClone && url == "" {
				return &usageError{fmt.Errorf("give the repository's URL, or pass --no-clone")}
			}
			path := filepath.Join(ws.Owner, config.TeamFile)
			before, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := fmt.Sprintf("\n[projects.%s]\n", alias)
			if dir != alias {
				text += fmt.Sprintf("directory = %q\n", dir)
			}
			if url != "" {
				text += fmt.Sprintf("repo = %q\n", url)
			}
			if err := os.WriteFile(path, append(bytes.Clone(before), text...), 0o644); err != nil {
				return err
			}
			c2, err := config.Load(ws.Owner)
			if err != nil {
				os.WriteFile(path, before, 0o644)
				return fmt.Errorf("%q cannot be a project name, so nothing was changed: %w", alias, err)
			}
			if dir != "." {
				if _, err := app.EnsureIgnored(filepath.Join(ws.Owner, ".gitignore"), []string{"/" + dir}); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if !noClone {
				if err := cloneProjects(cmd.Context(), ws.Owner, c2, out, cmd.ErrOrStderr()); err != nil {
					return err
				}
				if _, err := app.InstallGitHooks(cmd.Context(), ws.Owner, c2); err != nil {
					return err
				}
			}
			if _, err := compose.Apply(cmd.Context(), ws.Owner, c2.Workspace.Name, c2.Workspace.Namespace); err != nil {
				return err
			}
			fmt.Fprintf(out, "Added project %s. Next: fill in its servers and install in workspace.toml, run aw doctor --projects, and commit.\n", alias)
			return nil
		}),
	}
	cmd.Flags().StringVar(&directory, "directory", "", "where the repository lives (default: the alias)")
	cmd.Flags().BoolVar(&noClone, "no-clone", false, "do not clone it now")
	return cmd
}

// collisionKey matches the variables that must differ between two lanes.
var collisionKey = regexp.MustCompile(`^AW_HOSTNAME$|^AW_PORT_|DATABASE|_DB_|REDIS|BUCKET|^PORT$|_URL$`)

type rehearsal struct {
	exe, owner string
	collisions []string
}

func (r *rehearsal) aw(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, r.exe, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "AW_LAUNCHED=1", "PWD="+dir)
	var buf, errBuf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &errBuf
	if err := c.Run(); err != nil {
		return buf.String(), fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return buf.String(), nil
}

func (r *rehearsal) collide(format string, args ...any) {
	r.collisions = append(r.collisions, fmt.Sprintf(format, args...))
}

func parseEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

func newRehearseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rehearse [lane]",
		Short: "Create two lanes at once, start their servers, check they do not collide, and clean up",
		Long: `Create two lanes at the same time, run their setup and start their servers, then check that the lanes
do not collide: ports, hostnames and data must differ, and nothing may be written into the owner
checkout. Each project's smoke command runs if it has one. Both lanes are then torn down, and aw
confirms that nothing is left behind.

With a lane, or from inside one, it rehearses that lane's proposal as the lane has it now, committed
or not, in a throwaway copy of the workspace with its own name, ports and containers. Your checkout,
containers and configuration are not touched, and the copy is removed afterwards. The agent onboarding
a workspace may do this for its own lane.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			ctx, stop := context.WithTimeout(cmd.Context(), 20*time.Minute)
			defer stop()
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			if len(args) == 1 || ws.LaneRoot != "" {
				return rehearseLane(cmd, args)
			}
			if why := agentContext(); why != "" {
				return fmt.Errorf("aw rehearse in the owner checkout is only for the human, and this looks like an agent session (%s) → from your own onboarding lane, run bin/aw rehearse", why)
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			r := &rehearsal{exe: exe, owner: ws.Owner}
			out := cmd.OutOrStdout()
			repos := ownerRepos(ws, c)
			snapshot := func() string {
				var b strings.Builder
				for _, dir := range repos {
					s, _ := gitrun.In(dir).Run(ctx, "status", "--porcelain")
					b.WriteString(dir + "\n" + s)
				}
				return b.String()
			}
			before := snapshot()
			projects, err := scanProjects(ws, c)
			if err != nil {
				return err
			}
			for _, p := range projects {
				for _, f := range p.Findings {
					if f.Kind == "hardcoded-port" || f.Kind == "fixed-database" || f.Kind == "fixed-redis" {
						r.collide("%s %s:%d: %s", f.Project, f.File, f.Line, f.Message)
					}
				}
			}
			names := []string{"rehearse first", "rehearse second"}
			done := make(chan laneRun, 2)
			for _, purpose := range names {
				go func() {
					text, err := r.aw(ctx, ws.Owner, "attach", purpose, "--json")
					var claim struct {
						Dir  string `json:"lane"`
						Name string `json:"name"`
					}
					if err == nil {
						err = json.Unmarshal([]byte(text), &claim)
					}
					done <- laneRun{claim.Name, claim.Dir, err}
				}()
			}
			var lanesMade []laneRun
			var failed error
			for range names {
				l := <-done
				if l.err != nil {
					failed = l.err
					continue
				}
				lanesMade = append(lanesMade, l)
			}
			err = failed
			if err == nil {
				err = r.exercise(ctx, ws, c, lanesMade, out)
			}
			fmt.Fprintln(out, "Tearing down the two lanes.")
			if terr := r.teardown(ctx, ws, c, lanesMade); terr != nil {
				r.collide("left behind: %v", terr)
			}
			if after := snapshot(); after != before {
				r.collide("the rehearsal wrote into the owner checkout")
			}
			for _, x := range r.collisions {
				fmt.Fprintf(cmd.ErrOrStderr(), "collision: %s\n", ui.Clean(x))
			}
			if err != nil {
				return err
			}
			if len(r.collisions) > 0 {
				return &silentError{fmt.Errorf("%d collisions", len(r.collisions))}
			}
			fmt.Fprintln(out, "The rehearsal passed: the lanes did not collide, and nothing is left behind.")
			return nil
		}),
	}
}

// rehearseLane rehearses a lane's proposal in a throwaway copy of the workspace.
func rehearseLane(cmd *cobra.Command, args []string) error {
	m, done, err := openForRead()
	if err != nil {
		return err
	}
	defer done()
	if m.Store == nil {
		return errors.New("this workspace has no lanes yet → aw new starts one")
	}
	var l state.Lane
	if len(args) == 1 {
		l, err = m.Resolve(args[0])
	} else {
		l, err = laneArg(m, nil)
	}
	if err != nil {
		return err
	}
	if why := agentContext(); why != "" && (m.CallerLane() != l.Number || !lanes.IsOnboarding(l)) {
		return fmt.Errorf("an agent may only rehearse its own onboarding lane, and this looks like an agent session (%s) → ask the human to run aw rehearse %s", why, ui.Clean(l.Name))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	fmt.Fprintf(out, "Rehearsing lane %s in a throwaway copy of the workspace. Your checkout and containers are not touched.\n", ui.Clean(l.Name))
	copied, err := m.BuildRehearsal(ctx, l, randomHex(4), func(line string) { fmt.Fprintln(errOut, "aw: "+line) })
	if copied != nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := destroyRehearsal(cleanup, copied, errOut); err != nil {
				fmt.Fprintf(errOut, "aw: warning: the throwaway copy was not fully removed: %v\n", err)
			}
		}()
	}
	if err != nil {
		return err
	}
	copied.Started = true
	env := rehearsalEnv(exe, copied.Owner)
	infra := infraOf(&workspace.Workspace{Owner: copied.Owner}, copied.Cfg)
	if infra.Configured() {
		c := exec.CommandContext(ctx, exe, "infra", "up")
		c.Dir, c.Env, c.Stdout, c.Stderr = copied.Owner, env, errOut, errOut
		if err := c.Run(); err != nil {
			return fmt.Errorf("the proposal's shared infrastructure did not start: %w", err)
		}
	}
	c := exec.CommandContext(ctx, exe, "rehearse")
	c.Dir, c.Env, c.Stdout, c.Stderr = copied.Owner, env, out, errOut
	if err := c.Run(); err != nil {
		return &silentError{fmt.Errorf("the rehearsal of lane %s failed: %w", l.Name, err)}
	}
	return nil
}

// rehearsalEnv runs aw in the copy as nobody's session, with this very binary.
func rehearsalEnv(exe, dir string) []string {
	drop := []string{"AW_SESSION=", "AW_SESSION_PID=", "AW_TOOL_PATH=", "AW_LAUNCHED=", "PWD="}
	for _, e := range harnessEnv {
		drop = append(drop, e.env+"=")
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return slices.ContainsFunc(drop, func(p string) bool { return strings.HasPrefix(kv, p) })
	})
	return append(env, "AW_LAUNCHED=1", "AW_TOOL_PATH="+exe, "PWD="+dir)
}

// destroyRehearsal removes everything the copy made: its containers, infrastructure and files.
func destroyRehearsal(ctx context.Context, r *lanes.Rehearsal, out io.Writer) error {
	var errs []error
	ws := &workspace.Workspace{Owner: r.Owner}
	if r.Cfg != nil && r.Started {
		for _, name := range slices.Sorted(mapsKeys(r.Cfg.Projects)) {
			if r.Cfg.Projects[name].Runner != "devcontainer" {
				continue
			}
			d, err := devcontainerIn(ws, r.Cfg, name)
			if err == nil {
				err = d.Destroy(ctx)
			}
			errs = append(errs, err)
		}
		if infra := infraOf(ws, r.Cfg); infra.Configured() {
			errs = append(errs, infra.Destroy(ctx, out))
		}
	}
	errs = append(errs, r.Remove())
	return errors.Join(errs...)
}

type laneRun struct {
	name, dir string
	err       error
}

// exercise sets up both lanes, starts their servers, and compares what they got.
func (r *rehearsal) exercise(ctx context.Context, ws *workspace.Workspace, c *config.Config, lanesMade []laneRun, out io.Writer) error {
	fmt.Fprintf(out, "Claimed lanes %s and %s.\n", lanesMade[0].name, lanesMade[1].name)
	root := func(l laneRun) string { return filepath.Join(ws.Owner, ".worktrees", l.dir) }
	envs := map[string][2]map[string]string{}
	for _, name := range slices.Sorted(mapsKeys(c.Projects)) {
		var pair [2]map[string]string
		for i, l := range lanesMade {
			text, err := r.aw(ctx, root(l), "run", name, "env")
			if err != nil {
				return fmt.Errorf("setting up lane %s: %w", l.name, err)
			}
			pair[i] = parseEnv(text)
		}
		envs[name] = pair
		if smoke := c.Projects[name].Smoke; smoke != "" {
			for _, l := range lanesMade {
				if _, err := r.aw(ctx, root(l), "run", name, "sh", "-c", smoke); err != nil {
					r.collide("%s's smoke command failed in lane %s: %v", name, l.name, err)
				}
			}
		}
	}
	rs := map[string]int{}
	for name, res := range c.Resources {
		if res.Kind == "process" {
			rs[name] = res.Capacity
		}
	}
	if c.Lanes.ServerCapacity > 0 && implicitServers(c) {
		rs["servers"] = c.Lanes.ServerCapacity
	}
	for _, name := range slices.Sorted(mapsKeys(rs)) {
		if rs[name] >= 2 {
			for _, l := range lanesMade {
				if _, err := r.aw(ctx, root(l), "serve", name); err != nil {
					r.collide("resource %s did not start in lane %s (a fixed port?): %v", name, l.name, err)
				}
			}
			for _, l := range lanesMade {
				r.aw(ctx, root(l), "stop", name)
			}
		} else {
			for _, l := range lanesMade {
				if _, err := r.aw(ctx, root(l), "serve", name); err != nil {
					r.collide("resource %s did not start in lane %s: %v", name, l.name, err)
				}
				r.aw(ctx, root(l), "stop", name)
			}
		}
	}
	for _, name := range slices.Sorted(mapsKeys(envs)) {
		a, b := envs[name][0], envs[name][1]
		for _, key := range slices.Sorted(mapsKeys(a)) {
			// A value both inherited from this shell is not something the lanes were given.
			if collisionKey.MatchString(key) && a[key] != "" && a[key] == b[key] && a[key] != os.Getenv(key) {
				r.collide("%s: both lanes have %s=%s", name, key, a[key])
			}
		}
	}
	return nil
}

func implicitServers(c *config.Config) bool {
	claimed := map[string]bool{}
	for _, r := range c.Resources {
		for _, s := range r.Servers {
			claimed[s] = true
		}
	}
	for name, p := range c.Projects {
		for s := range p.Servers {
			if !claimed[name+"."+s] {
				return true
			}
		}
	}
	return false
}

// teardown removes the two lanes and what they made, and confirms that nothing is left. Whatever a
// lane's setup wrote is committed first, because aw discard rightly refuses to lose uncommitted work.
func (r *rehearsal) teardown(ctx context.Context, ws *workspace.Workspace, c *config.Config, made []laneRun) error {
	dir, err := ws.StateDir()
	if err != nil {
		return err
	}
	var left []string
	for _, l := range made {
		root := filepath.Join(ws.Owner, ".worktrees", l.dir)
		worktrees := []string{root}
		branch := c.Lanes.BranchPrefix + l.name
		for _, name := range slices.Sorted(mapsKeys(c.Projects)) {
			p := c.Projects[name]
			if p.Directory != "." && p.Lanes != "shared" {
				worktrees = append(worktrees, filepath.Join(ws.Owner, p.Directory, ".worktrees", l.dir))
			}
		}
		for _, w := range worktrees {
			if _, err := os.Stat(w); err != nil {
				continue
			}
			g := gitrun.In(w)
			g.Run(ctx, "add", "-A")
			g.Run(ctx, "-c", "user.name=aw", "-c", "user.email=aw@localhost", "commit", "-q", "--no-verify", "-m", "rehearsal")
		}
		if _, err := r.aw(ctx, ws.Owner, "discard", l.name, "--yes"); err != nil {
			left = append(left, err.Error())
		}
		for _, w := range worktrees {
			if _, err := os.Stat(w); err == nil {
				left = append(left, w)
			}
		}
		for _, repo := range ownerRepos(ws, c) {
			g := gitrun.In(repo)
			if ok, _ := g.OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); ok {
				g.Run(ctx, "branch", "-D", branch)
			}
			refs, _ := g.Run(ctx, "for-each-ref", "--format=%(refname)", "refs/aw/archive/"+l.name)
			for _, ref := range strings.Fields(refs) {
				g.Run(ctx, "update-ref", "-d", ref)
			}
		}
		s, err := state.Open(dir, state.ReadOnly)
		if err != nil {
			continue
		}
		if ls, err := state.LaneByName(s.DB(), l.name); err == nil && ls.State != state.LanePool {
			left = append(left, "lane "+l.name)
		}
		s.Close()
	}
	if len(left) > 0 {
		return errors.New(strings.Join(left, ", "))
	}
	return nil
}
