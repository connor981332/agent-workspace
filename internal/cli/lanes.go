package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func manager(w *writeContext) *lanes.Manager {
	return &lanes.Manager{WS: w.ws, Cfg: w.cfg, Store: w.store}
}

// laneArg resolves the lane a command names, or the lane it runs in.
func laneArg(m *lanes.Manager, args []string) (state.Lane, error) {
	if len(args) == 1 {
		return m.Resolve(args[0])
	}
	n := m.CallerLane()
	if n == 0 {
		return state.Lane{}, errors.New("this runs in the owner checkout, not in a lane → name the lane, or run it from the lane root")
	}
	return state.LaneByNumber(m.Store.DB(), n)
}

func newSweepCmd() *cobra.Command {
	var apply, asJSON bool
	var retry int
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Show which lanes are proven merged, and with --apply recycle them",
		Long: `Show, for every lane, whether it is kept, recycled or destroyed, and why.

A lane is recycled only when every repository in it is clean and proven merged: its head is in the
base branch, or its commits are patch-equivalent to the base's, or (with forge support) the forge
reports exactly that head merged. A gone remote branch, a handoff or a quiet session proves
nothing. Recycled lanes return to the warm pool; lanes beyond [lanes] pool, or older than
[lanes] retention in it, are removed. Without --apply nothing is changed.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			if retry > 0 {
				apply = true
			}
			rep, err := m.Sweep(cmd.Context(), lanes.SweepOptions{Apply: apply, Caller: m.CallerLane(), Retry: retry})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if rep.Lanes == nil {
					rep.Lanes = []*lanes.Decision{}
				}
				if err := writeJSON(out, rep); err != nil {
					return err
				}
			} else {
				if len(rep.Lanes) == 0 {
					fmt.Fprintln(out, "No lanes.")
				}
				for _, d := range rep.Lanes {
					name := d.Name
					if name == "" {
						name = "(pool)"
					}
					result := ""
					if d.Result != "" {
						result = " [" + d.Result + "]"
					}
					fmt.Fprintf(out, "%s %-20s %-8s %s%s\n", d.Dir, ui.Clean(name), d.Action, ui.Clean(d.Reason), result)
					if d.Error != "" {
						fmt.Fprintf(out, "   error: %s\n", ui.Clean(d.Error))
					}
				}
				for _, e := range rep.Open {
					fmt.Fprintf(out, "Unfinished: %s of lane %02d failed (%s) → aw sweep --apply --retry %d\n", e.Kind, e.Lane, ui.Clean(e.Detail), e.ID)
				}
				if !apply {
					n := 0
					for _, d := range rep.Lanes {
						if d.Action == lanes.ActionRecycle || d.Action == lanes.ActionDestroy {
							n++
						}
					}
					if n > 0 {
						fmt.Fprintf(out, "Nothing was changed. Run aw sweep --apply to recycle or remove %d lane(s).\n", n)
					}
				}
			}
			for _, d := range rep.Lanes {
				if d.Result == "failed" {
					return &silentError{fmt.Errorf("some lanes could not be cleaned up")}
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "recycle and remove the lanes the preview lists")
	cmd.Flags().IntVar(&retry, "retry", 0, "retry the cleanup that failed with this journal id")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the decisions as JSON")
	return cmd
}

func newDiscardCmd() *cobra.Command {
	var yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "discard <lane>",
		Short: "Abandon a lane's unmerged work: archive its branches, then remove it",
		Long: `Abandon a lane's unmerged work. Every branch of the lane is archived under
refs/aw/archive/<name>/<time> first, so nothing committed is lost; then the lane is removed. A lane
with uncommitted work is refused. It asks for confirmation on a terminal; elsewhere it needs --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := m.Resolve(args[0])
			if err != nil {
				return err
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return fmt.Errorf("aw discard removes lane %s; confirm with --yes", l.Name)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Discard lane %s (%q)? Its branches are archived first. [y/N] ", l.Name, l.Purpose)
				answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
					return errors.New("not discarded")
				}
			}
			archived, err := m.Discard(cmd.Context(), l, time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"lane": l.Name, "archived": archived})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Discarded lane %s. Archived:\n", l.Name)
			for _, a := range archived {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", a)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what was archived as JSON")
	return cmd
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newParkCmd(unpark bool) *cobra.Command {
	use, short := "park [lane]", "Shelve a lane: it is protected from cleanup until unparked"
	if unpark {
		use, short = "unpark [lane]", "Return a parked lane to work"
	}
	var asJSON bool
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := laneArg(m, args)
			if err != nil {
				return err
			}
			if unpark {
				l, err = m.Unpark(cmd.Context(), l)
			} else {
				l, err = m.Park(cmd.Context(), l)
			}
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), l)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Lane %s is %s.\n", l.Name, l.State)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the lane as JSON")
	return cmd
}

func newFinishCmd() *cobra.Command {
	var o lanes.FinishOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   `finish --summary "…" [--plan <path>] [lane]`,
		Short: "Hand this lane off for review: record its exact heads and a handoff note",
		Long: `Hand the lane off for review. It records the exact head of every repository and the handoff
note (the summary, and the plan if given), and moves the lane to ready, where it stays protected
until its work is proven merged. It refuses, listing every problem, when a repository has
uncommitted work, is not on the lane's branch, or changes while it runs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			l, err := laneArg(m, args)
			if err != nil {
				return err
			}
			f, err := m.Finish(cmd.Context(), l, o)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), f)
			}
			if f.Already {
				fmt.Fprintf(cmd.OutOrStdout(), "Lane %s was already handed off at these heads.\n", f.Lane.Name)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Lane %s is ready for review. It stays protected until its work is proven merged.\n", f.Lane.Name)
			return nil
		}),
	}
	cmd.Flags().StringVar(&o.Summary, "summary", "", "what changed, and what the human should verify")
	cmd.Flags().StringVar(&o.Plan, "plan", "", "the plan the work followed, a file inside the lane")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the handoff as JSON")
	return cmd
}
