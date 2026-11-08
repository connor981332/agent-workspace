package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newSetupCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install aw's Git hooks and harness hooks in this workspace",
		Long: `Point every repository's core.hooksPath at aw's hooks dispatcher, which refuses ref updates a lane may
not make and then runs the team's own hook of the same name. Merge aw's guard hooks into each
harness's project configuration, keeping other settings. Safe to rerun.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			results, err := app.InstallGitHooks(context.Background(), ws.Owner, c)
			if err != nil {
				return err
			}
			configs, err := app.InstallHarnessConfig(ws.Owner)
			if err != nil {
				return err
			}
			instructions, err := compose.Apply(cmd.Context(), ws.Owner, c.Workspace.Name, c.Workspace.Namespace)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"repos": results, "harness_config": configs, "instructions": instructions})
			}
			out := cmd.OutOrStdout()
			for _, r := range results {
				switch {
				case r.Missing:
					fmt.Fprintf(os.Stderr, "%s is not cloned yet, so it has no hooks\n", ui.Clean(r.Repo))
				case r.Installed:
					fmt.Fprintf(out, "%s: installed the hooks dispatcher\n", ui.Clean(r.Repo))
				default:
					fmt.Fprintf(out, "%s: already set up\n", ui.Clean(r.Repo))
				}
			}
			changed := false
			for _, c := range configs {
				if c.Changed {
					changed = true
					fmt.Fprintf(out, "%s: wrote aw's hooks\n", ui.Clean(c.Path))
				}
			}
			for _, r := range instructions {
				switch {
				case r.Edited:
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", r.Note)
				case r.Changed && r.Path != compose.PersonalPath:
					changed = true
					fmt.Fprintf(out, "%s: wrote the instructions\n", r.Path)
				case r.Changed:
					fmt.Fprintf(out, "%s: wrote your personal instructions (Git ignores this file)\n", r.Path)
				}
			}
			if changed {
				fmt.Fprintln(out, "Next: commit these files, so lanes get them. Codex asks once to trust project hooks, and Grok to trust the folder (/hooks-trust).")
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
