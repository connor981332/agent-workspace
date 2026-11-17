package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/ui"
)

func newArchivesCmd() *cobra.Command {
	var asJSON bool
	var restore, branch string
	cmd := &cobra.Command{
		Use:   "archives [--restore <name>[/<time>] [--branch b]]",
		Short: "List the work aw archived, and with --restore make a branch from it",
		Long: `List what aw discard, aw reset-branch and the orphan cleanup archived under
refs/aw/archive/<name>/<time>, newest first, with each repository's tip.

With --restore, make a branch at an archive's tips in every repository it holds: restored/<name>
unless --branch names another. <name> alone means its newest archive. A lane then starts from it
with aw new "<purpose>" --branch <branch>. Restoring is the human's.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			out := cmd.OutOrStdout()
			if restore != "" {
				a, err := m.RestoreArchive(cmd.Context(), restore, branch)
				if err != nil {
					return err
				}
				if branch == "" {
					branch = "restored/" + a.Name
				}
				if asJSON {
					return writeJSON(out, map[string]any{"restored": a, "branch": branch})
				}
				var repos []string
				for _, r := range a.Repos {
					repos = append(repos, r.Repo)
				}
				fmt.Fprintf(out, "Restored %s as branch %s in %s.\nNext: aw new \"<purpose>\" --branch %s\n",
					ui.Clean(a.ID), ui.Clean(branch), ui.Clean(strings.Join(repos, ", ")), ui.Clean(branch))
				return nil
			}
			all, err := m.Archives(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, map[string]any{"archives": all})
			}
			if len(all) == 0 {
				fmt.Fprintln(out, "Nothing is archived.")
				return nil
			}
			for _, a := range all {
				fmt.Fprintln(out, ui.Clean(a.ID))
				for _, r := range a.Repos {
					head := r.Head
					if len(head) > 12 {
						head = head[:12]
					}
					fmt.Fprintf(out, "   %s: %s %s\n", ui.Clean(r.Repo), head, ui.Clean(r.Subject))
				}
			}
			fmt.Fprintln(out, "\nTo pick one up again: aw archives --restore <name>, then aw new \"<purpose>\" --branch restored/<name>")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().StringVar(&restore, "restore", "", "make a branch from this archive: <name> or <name>/<time>")
	cmd.Flags().StringVar(&branch, "branch", "", "the branch --restore makes (default restored/<name>)")
	return cmd
}
