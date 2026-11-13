package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newNoteCmd() *cobra.Command {
	var done int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   `note ["<text>"] [--done <n>]`,
		Short: "Keep a note on this lane of something left to do, such as a temporary change to undo",
		Long: `Write down, on the lane, something that has to happen before the work is done: a temporary CI
schedule to delete, a test branch to remove, a reviewer to wait for. Notes outlive the session's
memory: aw recap shows them, a session starting or resuming in the lane is told them, and aw finish
warns while any is open.

With no text, it lists the lane's open notes. --done <n> marks one done.`,
		Args: cobra.MaximumNArgs(1),
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
			out := cmd.OutOrStdout()
			switch {
			case done > 0 && len(args) > 0:
				return &usageError{errors.New("give a note's text, or --done with its number, not both")}
			case done > 0:
				err := w.store.Write(cmd.Context(), func(tx *sql.Tx) error { return state.CloseNote(tx, l.Number, l.Generation, done) })
				if errors.Is(err, state.ErrNoNote) {
					return fmt.Errorf("lane %s has no open note %d → aw note lists them", l.Name, done)
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Note %d is done.\n", done)
				return nil
			case len(args) == 1:
				text := strings.TrimSpace(args[0])
				if text == "" {
					return &usageError{errors.New(`a note needs text: aw note "delete the temporary CI schedule"`)}
				}
				var id int
				if err := w.store.Write(cmd.Context(), func(tx *sql.Tx) error {
					id, err = state.AddNote(tx, l.Number, l.Generation, text)
					return err
				}); err != nil {
					return err
				}
				fmt.Fprintf(out, "Noted on lane %s as %d. aw note --done %d when it is done.\n", l.Name, id, id)
				return nil
			}
			notes, err := state.OpenNotes(w.store.DB(), l.Number, l.Generation)
			if err != nil {
				return err
			}
			if asJSON {
				if notes == nil {
					notes = []state.Note{}
				}
				return writeJSON(out, map[string]any{"lane": l.Name, "notes": notes})
			}
			if len(notes) == 0 {
				fmt.Fprintf(out, "Lane %s has no open notes.\n", l.Name)
			}
			for _, n := range notes {
				fmt.Fprintf(out, "%s. %s\n", strconv.Itoa(n.ID), ui.Clean(n.Text))
			}
			return nil
		}),
	}
	cmd.Flags().IntVar(&done, "done", 0, "mark this note done")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the open notes as JSON")
	return cmd
}
