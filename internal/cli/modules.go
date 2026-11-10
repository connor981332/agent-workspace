package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/modules"
	"github.com/connor981332/agent-workspace/internal/plans"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

func infraOf(ws *workspace.Workspace, c *config.Config) *runners.Infra {
	services, ports := modules.Services(ws.Owner)
	return runners.NewInfra(ws.Owner, c.Workspace.Namespace, services, ports, c.Ports.Offset)
}

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "add", Short: "Add a module or a project to this workspace"}
	cmd.AddCommand(newAddProjectCmd(), &cobra.Command{
		Use:   "module <name>…",
		Short: "Install first-party modules: their files, configuration and instructions",
		Args:  cobra.MinimumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if why := unguardedAgent(); why != "" {
				return &commandError{fmt.Errorf("aw add is only for the human, and this looks like an agent session (%s) → ask the human to run it in their own terminal", why)}
			}
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			if ws.LaneRoot != "" {
				return fmt.Errorf("modules are installed in the owner checkout → run this in %s", ui.Clean(ws.Owner))
			}
			out := cmd.OutOrStdout()
			for _, name := range args {
				res, err := modules.Install(ws.Owner, c.Workspace.Namespace, c.Workspace.Name, name)
				if err != nil {
					return err
				}
				if len(res.Gitignore) > 0 {
					if _, err := app.EnsureIgnored(filepath.Join(ws.Owner, ".gitignore"), res.Gitignore); err != nil {
						return err
					}
				}
				for _, f := range res.Files {
					fmt.Fprintf(out, "%s: wrote %s\n", name, f)
				}
			}
			if services, _ := modules.Services(ws.Owner); len(services) > 0 {
				base := filepath.Join(ws.Owner, "infra", "compose.yaml")
				if _, err := os.Stat(base); err != nil {
					if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
						return err
					}
					infra := infraOf(ws, c)
					if err := os.WriteFile(base, []byte(runners.BaseCompose(infra.Project, infra.Network)), 0o644); err != nil {
						return err
					}
				}
			}
			if c, err = config.Load(ws.Owner); err != nil {
				return err
			}
			if _, err := compose.Apply(cmd.Context(), ws.Owner, c.Workspace.Name, c.Workspace.Namespace); err != nil {
				return err
			}
			fmt.Fprintln(out, "Next: review and commit these files.")
			return nil
		}),
	})
	return cmd
}

func newModulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "modules",
		Short: "List the first-party modules, and which this workspace has",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			var installed []string
			if ws, _, err := loadConfig(); err == nil {
				installed = modules.Installed(ws.Owner)
			}
			list, err := modules.Available()
			if err != nil {
				return err
			}
			for _, m := range list {
				mark := " "
				if slices.Contains(installed, m.Name) {
					mark = "*"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %-16s %s\n", mark, m.Name, m.Summary)
			}
			return nil
		}),
	}
}

func newEjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "eject <module or file>",
		Short: "Stop aw managing a module's files, or one file, so that the team owns them",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			released, err := modules.Eject(ws.Owner, c.Workspace.Namespace, c.Workspace.Name, args[0])
			if err != nil {
				return err
			}
			for _, f := range released {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is yours now: aw will not manage it\n", f)
			}
			return nil
		}),
	}
}

func newInfraCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "infra", Short: "Start, inspect or take down the shared infrastructure"}
	verb := func(use, short string, f func(ctx context.Context, i *runners.Infra, out io.Writer, args []string) error) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, RunE: run(func(cmd *cobra.Command, args []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			i := infraOf(ws, c)
			if !i.Configured() {
				return fmt.Errorf("this workspace has no shared infrastructure → aw modules lists what can provide it")
			}
			return f(cmd.Context(), i, cmd.OutOrStdout(), args)
		})}
	}
	down := verb("down", "Take down the shared infrastructure, keeping its volumes (the human's)", func(ctx context.Context, i *runners.Infra, out io.Writer, _ []string) error {
		if why := unguardedAgent(); why != "" {
			return &commandError{fmt.Errorf("aw infra down is only for the human, and this looks like an agent session (%s) → ask the human to run it", why)}
		}
		return i.Down(ctx, out)
	})
	cmd.AddCommand(
		verb("up", "Start whatever is not running. When everything is, it does nothing", func(ctx context.Context, i *runners.Infra, out io.Writer, _ []string) error { return i.Up(ctx, out) }),
		verb("ps", "Show the shared containers", func(ctx context.Context, i *runners.Infra, out io.Writer, _ []string) error {
			return i.Status(ctx, out)
		}),
		verb("logs", "Show the shared containers' logs", func(ctx context.Context, i *runners.Infra, out io.Writer, args []string) error {
			return i.Logs(ctx, out, args)
		}),
		down)
	return cmd
}

type webhookRecord struct {
	Lane  int    `json:"lane"`
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	Start string `json:"start"`
	Log   string `json:"log"`
}

func webhookKey(provider string) string { return "webhook:" + provider }

func newWebhookCmd() *cobra.Command {
	var list, stop bool
	cmd := &cobra.Command{
		Use:   "webhook <provider> | --list",
		Short: "Send a provider's sandbox webhooks to this lane",
		Long: `Send a provider's sandbox webhooks to this lane's server, on the seat it holds. The most recent lane
to register receives them: registering stops the forwarder of the lane before, which is told.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			if list {
				for _, name := range slices.Sorted(mapsKeys(w.cfg.Webhooks)) {
					raw, _ := w.store.Meta(webhookKey(name))
					who := "nobody"
					var rec webhookRecord
					if json.Unmarshal([]byte(raw), &rec) == nil && rec.Name != "" {
						who = ui.Clean(rec.Name)
					}
					fmt.Fprintf(out, "%s → %s\n", name, who)
				}
				return nil
			}
			if len(args) != 1 {
				return &usageError{fmt.Errorf("name a provider, or pass --list")}
			}
			hook, ok := w.cfg.Webhooks[args[0]]
			if !ok {
				return fmt.Errorf("there is no [webhooks.%s] in workspace.toml", args[0])
			}
			m := manager(w)
			n := m.CallerLane()
			name := "the owner"
			if n > 0 {
				l, err := state.LaneByNumber(w.store.DB(), n)
				if err != nil {
					return err
				}
				name = l.Name
			}
			raw, _ := w.store.Meta(webhookKey(args[0]))
			var previous webhookRecord
			if raw != "" && json.Unmarshal([]byte(raw), &previous) == nil && previous.PID > 0 {
				procs.Stop(procs.Proc{PID: previous.PID, Start: previous.Start, Log: previous.Log}, 2*time.Second)
				if previous.Lane != n && previous.Lane > 0 {
					msg := fmt.Sprintf("Lane %s now receives the %s webhooks, so this lane no longer does.", name, args[0])
					if err := w.store.Write(ctx, addNote(previous.Lane, msg)); err != nil {
						return err
					}
				}
			}
			if stop {
				if err := w.store.SetMeta(ctx, webhookKey(args[0]), ""); err != nil {
					return err
				}
				fmt.Fprintf(out, "Stopped the %s forwarder.\n", args[0])
				return nil
			}
			project, sname, s := serverOf(w.cfg, hook.Server)
			if s == nil {
				return fmt.Errorf("[webhooks.%s] server %q is not configured → name it as <project>.<server>", args[0], hook.Server)
			}
			seat := -1
			leases, err := state.Leases(w.store.DB())
			if err != nil {
				return err
			}
			for _, l := range leases {
				r, err := newBroker(w).Lookup(l.Resource)
				if err == nil && l.Lane == n && slices.Contains(r.Servers, project+"."+sname) {
					seat = l.Seat
				}
			}
			if seat < 0 {
				return fmt.Errorf("this lane holds no seat on the server that receives these webhooks → run aw serve first")
			}
			url := fmt.Sprintf("http://localhost:%d%s", port(w.cfg, s, seat), hook.Path)
			where, err := m.LocateIn(n, project)
			if err != nil {
				return err
			}
			dir, err := w.ws.StateDir()
			if err != nil {
				return err
			}
			log := filepath.Join(dir, "webhooks", args[0]+".log")
			p, err := procs.Start(ctx, procs.Spec{Command: strings.ReplaceAll(hook.Listen, "{url}", url), Dir: where.Dir, Env: where.Env, Log: log})
			if err != nil {
				return fmt.Errorf("the %s forwarder did not start: %w", args[0], err)
			}
			data, _ := json.Marshal(webhookRecord{Lane: n, Name: name, PID: p.PID, Start: p.Start, Log: log})
			if err := w.store.SetMeta(ctx, webhookKey(args[0]), string(data)); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s webhooks now come to %s, at %s.\n", args[0], ui.Clean(name), url)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&list, "list", false, "show which lane receives each provider's webhooks")
	cmd.Flags().BoolVar(&stop, "stop", false, "stop the provider's forwarder")
	return cmd
}

func mapsKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// personalPlans is where plans live before they are promoted (R83).
func personalPlans(namespace string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "agent-workspace", namespace, "plans")
}

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Create, list and promote plans"}
	enabled := func() (*workspace.Workspace, *config.Config, error) {
		ws, c, err := loadConfig()
		if err != nil {
			return nil, nil, err
		}
		if !c.Plans.Enabled {
			return nil, nil, fmt.Errorf("this workspace has opted out of plans ([plans] enabled = false)")
		}
		return ws, c, nil
	}
	var team bool
	create := &cobra.Command{
		Use:   "new <slug>",
		Short: "Start a plan in your own area (--team for the team's plans/)",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			ws, c, err := enabled()
			if err != nil {
				return err
			}
			if err := lanes.CheckName(args[0]); err != nil {
				return err
			}
			dir := personalPlans(c.Workspace.Namespace)
			if team {
				base := ws.Owner
				if ws.LaneRoot != "" {
					base = ws.LaneRoot
				}
				dir = filepath.Join(base, "plans")
			}
			path := filepath.Join(dir, args[0]+".md")
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", ui.Clean(path))
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			text := plans.Template(args[0], time.Now().Format("2006-01-02"))
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), ui.Clean(path))
			return nil
		}),
	}
	create.Flags().BoolVar(&team, "team", false, "write it to the team's plans/")
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "Show where the plans stand: the team's, and your own",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, c, err := enabled()
			if err != nil {
				return err
			}
			base := ws.Owner
			if ws.LaneRoot != "" {
				base = ws.LaneRoot
			}
			all, _ := plans.List(filepath.Join(base, "plans"))
			mine, _ := plans.List(personalPlans(c.Workspace.Namespace))
			all = append(all, mine...)
			sort.SliceStable(all, func(i, j int) bool { return all[i].Slug < all[j].Slug })
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"plans": all})
			}
			for _, p := range all {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s · next: %s\n", p.Slug, p.Fields["State"], ui.Clean(p.Fields["Next step"]))
			}
			return nil
		}),
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print the plans as JSON")
	promote := &cobra.Command{
		Use:   "promote <slug>",
		Short: "Move one of your plans to the team's plans/, and add it to the index",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			ws, c, err := enabled()
			if err != nil {
				return err
			}
			if err := lanes.CheckName(args[0]); err != nil {
				return err
			}
			base := ws.Owner
			if ws.LaneRoot != "" {
				base = ws.LaneRoot
			}
			from := filepath.Join(personalPlans(c.Workspace.Namespace), args[0]+".md")
			s, err := plans.Parse(from)
			if err != nil {
				return fmt.Errorf("%s: %w", ui.Clean(from), err)
			}
			to := filepath.Join(base, "plans", args[0]+".md")
			if _, err := os.Stat(to); err == nil {
				return fmt.Errorf("the team already has a plan named %s", args[0])
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			data, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			if err := os.WriteFile(to, data, 0o644); err != nil {
				return err
			}
			index := filepath.Join(base, "plans", "README.md")
			if _, err := os.Stat(index); err == nil {
				f, err := os.OpenFile(index, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					return err
				}
				fmt.Fprintf(f, "| [%s.md](%s.md) | %s | %s | |\n", args[0], args[0], s.Fields["State"], s.Fields["Updated"])
				f.Close()
			}
			if err := os.Remove(from); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moved it to plans/%s.md. Commit it, so the team has it.\n", args[0])
			return nil
		}),
	}
	cmd.AddCommand(create, list, promote)
	return cmd
}

func newCheckDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-docs [directory…]",
		Short: "Check the plan convention and the relative Markdown links, everywhere or under the directories",
		RunE: run(func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			if ws, err := workspace.Find(root); err == nil {
				root = ws.Owner
				if ws.LaneRoot != "" {
					root = ws.LaneRoot
				}
			}
			problems, err := plans.Check(root, args...)
			if err != nil {
				return err
			}
			if len(problems) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Docs check passed.")
				return nil
			}
			for _, p := range problems {
				fmt.Fprintln(cmd.ErrOrStderr(), p)
			}
			return &silentError{fmt.Errorf("%s problems", strconv.Itoa(len(problems)))}
		}),
	}
}

func addNote(lane int, msg string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error { return state.AddNotification(tx, lane, "webhook", msg) }
}
