package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newPullCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Fetch origin in every owner checkout, and fast-forward those on the default branch",
		Long: `Fetch origin in the workspace repository and in every project's owner checkout, in parallel.

A checkout on origin's default branch is fast-forwarded to it. One on another branch, or with
commits origin lacks, is only fetched and left as it is: aw pull never merges, rebases or switches.

New lanes start from origin's default branch as last fetched, and aw never fetches on its own, so
run this to start new lanes from the newest work. Existing lanes are not changed.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, cfg, err := loadConfig()
			if err != nil {
				return err
			}
			m := &lanes.Manager{WS: ws, Cfg: cfg}
			if !asJSON {
				m.Progress = func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), s) }
			}
			results := m.Pull(cmd.Context())
			failed := 0
			for _, r := range results {
				if r.Failed() {
					failed++
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if err := writeJSON(out, map[string]any{"repos": results}); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					line := fmt.Sprintf("%s: %s", ui.Clean(r.Repo), r.Outcome)
					if r.Base != "" && r.Outcome != "fetched only" {
						line += " with " + r.Base
					}
					if r.Detail != "" {
						line += " (" + ui.Clean(r.Detail) + ")"
					}
					fmt.Fprintln(out, line)
				}
			}
			if failed > 0 {
				return &silentError{errors.New("aw pull could not update every checkout")}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what happened in each checkout as JSON")
	return cmd
}
