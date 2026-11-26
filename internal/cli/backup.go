package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/ui"
)

func newBackupCmd() *cobra.Command {
	var laneName, restore string
	var list, asJSON bool
	cmd := &cobra.Command{
		Use:   "backup [<label>] | --list | --restore <label>",
		Short: "Save the lane's branch before rewriting its history, and restore it",
		Long: `Save the tip of the lane's branch, in every repository it has, before a rebase, a squash or a
rebuild: a lane may not make branches or tags of its own, so this is its backup. Backups are kept
under refs/aw/backup/<lane>/<label>, are never pushed, and <label> is the time when you give none.

--list lists them. --restore moves the lane's branch back to one, after saving where it was as a
backup of its own; it refuses while there is uncommitted work, which a restore would lose.

When the lane is removed, a backup its final branch or its base already holds is dropped; any other
is archived, so aw archives lists it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if (list || restore != "") && len(args) > 0 {
				return &usageError{errors.New("a label is saved without --list or --restore; --restore takes the label as its value")}
			}
			if list && restore != "" {
				return &usageError{errors.New("--list and --restore are separate")}
			}
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
				return fmt.Errorf("this runs in another lane, and a lane backs up only its own branch → run it from lane %s's root, or from the owner checkout", l.Name)
			}
			out := cmd.OutOrStdout()
			switch {
			case list:
				all, err := m.Backups(cmd.Context(), l)
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(out, map[string]any{"backups": all})
				}
				if len(all) == 0 {
					fmt.Fprintf(out, "Lane %s has no backups.\n", ui.Clean(l.Name))
					return nil
				}
				for _, b := range all {
					fmt.Fprintf(out, "%s  %s %s %s\n", ui.Clean(b.Label), ui.Clean(b.Repo), b.Head[:min(12, len(b.Head))], ui.Clean(b.Subject))
				}
				return nil
			case restore != "":
				saved, restored, err := m.RestoreBackup(cmd.Context(), l, restore, time.Now())
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(out, map[string]any{"restored": restored, "saved": saved})
				}
				if len(saved) > 0 {
					fmt.Fprintf(out, "Saved where the branch was as backup %s.\n", ui.Clean(saved[0].Label))
				}
				for _, b := range restored {
					fmt.Fprintf(out, "%s: restored to %s %s\n", ui.Clean(b.Repo), b.Head[:min(12, len(b.Head))], ui.Clean(b.Subject))
				}
				return nil
			}
			label := ""
			if len(args) == 1 {
				label = args[0]
			}
			saved, err := m.SaveBackup(cmd.Context(), l, label, time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, map[string]any{"saved": saved})
			}
			for _, b := range saved {
				fmt.Fprintf(out, "%s: saved %s %s as backup %s\n", ui.Clean(b.Repo), b.Head[:min(12, len(b.Head))], ui.Clean(b.Subject), ui.Clean(b.Label))
			}
			fmt.Fprintf(out, "Restore it with bin/aw backup --restore %s\n", ui.Clean(saved[0].Label))
			return nil
		}),
	}
	cmd.Flags().BoolVar(&list, "list", false, "list the lane's backups")
	cmd.Flags().StringVar(&restore, "restore", "", "move the lane's branch back to this backup")
	cmd.Flags().StringVar(&laneName, "lane", "", "the lane (default: the one this runs in)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print it as JSON")
	return cmd
}
