package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newSetupCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install the Git hooks dispatcher in every repository of this workspace",
		Long: `Point every repository's core.hooksPath at aw's hooks dispatcher, which refuses ref updates a lane may
not make and then runs the team's own hook of the same name. Safe to rerun.`,
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
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"repos": results})
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
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
