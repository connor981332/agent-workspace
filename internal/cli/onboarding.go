package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		Short: "Report what can be found out about the projects, for onboarding. It only reads files",
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
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"projects": projects})
			}
			for _, p := range projects {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): %s; %d environment variables, %d findings\n", p.Name, p.Directory, strings.Join(p.Stacks, ", "), len(p.EnvVars), len(p.Findings))
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
	var review bool
	var harness string
	cmd := &cobra.Command{
		Use:   "onboard [--review] [--harness h]",
		Short: "Have an agent design this workspace's configuration, in a lane, for you to review",
		Long: `Claim a lane and launch a harness there with the workspace-design skill. Its result stays on the
lane's branch until a person reviews it. With --review it compares the current configuration with the
repositories, and proposes changes only for what is new or has drifted.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			if harness == "" {
				harness = c.User.Harness
			}
			prompt := "Use the aw-workspace-design skill to design this workspace, then stop and report."
			purpose := "onboard this workspace"
			if review {
				prompt = "Use the aw-workspace-design skill, but only for what is new or has drifted: compare workspace.toml with aw inspect --json, and propose changes for a new project, a new server or a new dependency on data. Leave the rest. Then stop and report."
				purpose = "review onboarding"
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			args := append([]string{self, "new", purpose, "--harness", harness, "--"}, launch.PromptArgs(harness, prompt)...)
			if err := os.Chdir(ws.Owner); err != nil {
				return err
			}
			os.Setenv("AW_LAUNCHED", "1")
			return syscall.Exec(self, args, os.Environ())
		}),
	}
	cmd.Flags().BoolVar(&review, "review", false, "propose changes only for what is new or has drifted")
	cmd.Flags().StringVar(&harness, "harness", "", "the harness to launch")
	return cmd
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
		Use:   "rehearse",
		Short: "Create two lanes at once, start their servers, check they do not collide, and clean up",
		Long: `Create two lanes at the same time, run their setup and start their servers, then check that the lanes
do not collide: ports, hostnames and data must differ, and nothing may be written into the owner
checkout. Each project's smoke command runs if it has one. Both lanes are then torn down, and aw
confirms that nothing is left behind.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ctx, stop := context.WithTimeout(cmd.Context(), 20*time.Minute)
			defer stop()
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			if ws.LaneRoot != "" {
				return fmt.Errorf("aw rehearse runs in the owner checkout → run it in %s", ui.Clean(ws.Owner))
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
