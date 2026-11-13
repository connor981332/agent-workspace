package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newResetBranchCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "reset-branch [project…]",
		Short: "Start this lane's branches over from their base, archiving what they had",
		Long: `Start the lane's branch over from its base branch as last fetched, in every repository with
commits of the lane's own, or in the projects named. Each tip is archived under
refs/aw/archive/<lane>/<time> first, as aw discard does, so nothing committed is lost; a repository
with uncommitted work refuses the whole reset. Run it in the lane, such as when this session's lane
still holds an earlier purpose's commits.`,
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := laneArg(m, nil)
			if err != nil {
				return err
			}
			results, err := m.ResetBranch(cmd.Context(), l, args, time.Now())
			out := cmd.OutOrStdout()
			if asJSON {
				if results == nil {
					results = []lanes.ResetResult{}
				}
				if werr := writeJSON(out, map[string]any{"lane": l.Name, "reset": results}); werr != nil {
					return werr
				}
				return err
			}
			for _, r := range results {
				fmt.Fprintf(out, "%s: reset %s to %s (%s), after archiving its %d commit%s at %s → git branch <name> %s brings them back\n",
					ui.Clean(r.Repo), ui.Clean(r.Branch), ui.Clean(r.Base), short(r.To), r.Commits, plural(r.Commits), r.Archived, r.Archived)
			}
			if err == nil && len(results) == 0 {
				fmt.Fprintf(out, "Lane %s has no commits of its own to reset.\n", l.Name)
			}
			return err
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what was reset as JSON")
	return cmd
}

// leftoverNote warns a session that attached again with another purpose that its lane still has
// the earlier purpose's commits (plans/agent-feedback.md, item 10).
func leftoverNote(cmd *cobra.Command, m *lanes.Manager, l state.Lane) {
	ss, err := m.Status(cmd.Context(), lanes.StatusOptions{Only: l.Number})
	if err != nil || len(ss) == 0 {
		return
	}
	var work []string
	for _, r := range ss[0].Repos {
		if r.Ahead > 0 {
			work = append(work, fmt.Sprintf("%s: %d commit%s, %s", r.Project, r.Ahead, plural(r.Ahead), r.Push))
		}
	}
	if len(work) == 0 {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "aw: note: this session's lane %s still has the commits of its earlier purpose, %q:\n", l.Name, ui.Clean(l.Purpose))
	for _, w := range work {
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", ui.Clean(w))
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "  → to start the new work from the base, run bin/aw reset-branch, which archives them first; or bin/aw finish this lane, and attach from a new session")
}
