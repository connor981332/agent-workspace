package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/ui"
)

func newAdoptCmd() *cobra.Command {
	var laneName string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "adopt <remote-branch> [project…]",
		Short: "Continue an existing branch on origin in this lane",
		Long: `Continue an existing branch on origin in this lane: an open merge request, a spike, a teammate's
branch. aw fetches it, points the lane's own branch, aw/<lane>, at it, sets its upstream, and records
it as the name aw push pushes to, so recap, finish and sweep know the lane's merge request from the
start.

Without projects, it adopts the branch in every repository of the lane that has it on origin. It
refuses, changing nothing, while a repository has uncommitted changes or commits of the lane's own
that the branch does not hold.`,
		Args: cobra.MinimumNArgs(1),
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
				return fmt.Errorf("this runs in another lane, and a lane adopts a branch only into its own → run it from lane %s's root, or from the owner checkout", l.Name)
			}
			if !asJSON {
				m.Progress = func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "aw: "+s) }
			}
			adopted, err := m.Adopt(cmd.Context(), l, args[0], args[1:])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"adopted": adopted})
			}
			for _, a := range adopted {
				fmt.Fprintf(out, "%s: %s now continues origin/%s at %s\n", ui.Clean(a.Repo), ui.Clean(a.Branch), ui.Clean(a.Remote), a.Head[:min(12, len(a.Head))])
			}
			fmt.Fprintf(out, "Publish with bin/aw push, which pushes to %s.\n", ui.Clean(args[0]))
			return nil
		}),
	}
	cmd.Flags().StringVar(&laneName, "lane", "", "the lane (default: the one this runs in)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what happened in each repository as JSON")
	return cmd
}
