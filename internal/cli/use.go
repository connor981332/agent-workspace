package cli

import (
	"errors"
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/spf13/cobra"
)

func newUseCmd() *cobra.Command {
	var lane string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "use <project>… [--lane <lane>]",
		Short: "Check projects out in this lane, on its branch, with the projects they depend on",
		Long: `A lane checks out the projects in [lanes] eager, and those aw new --projects or aw attach
--projects named, when it is claimed. Any other project is checked out on first use: aw run and
aw serve do it, and so does this command. It creates each project's worktree on the lane's branch,
with the projects it depends_on, its secret links and local files, and the link from the lane
root, then runs its prepare. Its other setup steps wait for its first aw run, aw serve or team
command, as every project's do. It is safe to rerun.

In a lane it acts on that lane. An agent may run it for its own lane without being asked; --lane
names another, which only the human does.`,
		Args: cobra.MinimumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			n := m.CallerLane()
			if lane != "" {
				l, err := m.Resolve(lane)
				if err != nil {
					return err
				}
				if why := unguardedAgent(); why != "" && l.Number != n {
					return fmt.Errorf("an agent may only check projects out in its own lane, and this looks like an agent session (%s) outside lane %s → ask the human to run it", why, ui.Clean(l.Name))
				}
				n = l.Number
			}
			if n == 0 {
				return &usageError{errors.New("aw use checks projects out in a lane → run it in the lane, or name one with --lane")}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			progress := cmd.ErrOrStderr()
			m.Progress = func(line string) { fmt.Fprintln(progress, "aw: "+line) }
			u, err := m.Use(ctx, n, args)
			if err != nil {
				return err
			}
			// The added projects' prepare runs now, as it does at claim time, since it writes the env
			// files their commands read; their other steps wait for first use, unless setup is eager.
			var note string
			if len(u.Added) > 0 {
				var projects []string
				if lifecycle.Lazy(w.cfg) {
					projects = []string{}
				}
				switch err := runSetup(ctx, w, m, u.Lane, progress, projects); {
				case errors.Is(err, lifecycle.ErrRunning):
					note = "the lane's setup is still running, so their prepare runs on their first aw run or aw serve"
				case err != nil:
					return err
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), u)
			}
			out := cmd.OutOrStdout()
			switch {
			case len(u.Added) == 0:
				fmt.Fprintf(out, "Lane %s already has %s.\n", ui.Clean(u.Lane.Name), strings.Join(args, ", "))
			default:
				then := "Their setup runs on their first aw run or aw serve."
				if !lifecycle.Lazy(w.cfg) {
					then = "They are set up."
				}
				fmt.Fprintf(out, "Checked out %s in lane %s, on its branch. %s\n", strings.Join(u.Added, ", "), ui.Clean(u.Lane.Name), then)
			}
			if note != "" {
				fmt.Fprintf(out, "Note: %s.\n", note)
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&lane, "lane", "", "the lane to check the projects out in, if not this one")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what was checked out as JSON")
	return cmd
}
