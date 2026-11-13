package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/ui"
)

func newPushCmd() *cobra.Command {
	var as, laneName string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "push [--as <name>] [project…]",
		Short: "Push the lane's branch to origin, under the team's branch name if it differs",
		Long: `Push this lane's branch to origin and set its upstream. A lane keeps its own branch, aw/<lane>,
which aw tracks its work by; --as pushes it under the name your team's conventions want instead
(git push -u origin aw/<lane>:<name>), and aw remembers that name, so recap, finish and sweep find
the merge request, and the next aw push uses it again.

Without projects, it pushes every repository with commits of the lane's own, and every one pushed
before. Pushing follows the workspace's publication policy, like git push.`,
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			var named []string
			if laneName != "" {
				named = []string{laneName}
			}
			l, err := laneArg(m, named)
			if err != nil {
				return err
			}
			if own := m.CallerLane(); own != 0 && own != l.Number {
				return fmt.Errorf("this runs in another lane, and a lane pushes only its own branch → run it from lane %s's root, or from the owner checkout", l.Name)
			}
			if !asJSON {
				m.Progress = func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "aw: "+s) }
			}
			results, err := m.Push(cmd.Context(), l, as, args)
			if err != nil {
				return err
			}
			failed := false
			for _, r := range results {
				failed = failed || r.Failed()
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if err := writeJSON(out, map[string]any{"repos": results}); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					line := r.Outcome
					if r.Repo != "" {
						line = fmt.Sprintf("%s: %s", ui.Clean(r.Repo), r.Outcome)
					}
					if r.Outcome == "pushed" {
						line += fmt.Sprintf(" %s to origin/%s", ui.Clean(r.Branch), ui.Clean(r.Remote))
					}
					if r.Detail != "" {
						line += " (" + ui.Clean(r.Detail) + ")"
					}
					fmt.Fprintln(out, line)
				}
				if as != "" && !failed {
					fmt.Fprintln(out, "Push again with bin/aw push; plain git push refuses while the names differ.")
				}
			}
			if failed {
				return &silentError{errors.New("aw push could not push every repository")}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&as, "as", "", "the branch name on origin, when it differs from the lane's")
	cmd.Flags().StringVar(&laneName, "lane", "", "the lane (default: the one this runs in)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what happened in each repository as JSON")
	return cmd
}
