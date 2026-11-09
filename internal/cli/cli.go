// Package cli is aw's command tree: flags, human and --json output, and exit codes.
//
// The hot path (aw hook and aw git-hook) never reaches this package. cmd/aw dispatches those
// before Main is called, so building the command tree costs nothing on a guard decision.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Main runs the command line in args (without the program name) and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	if c, ok := teamCommandNamed(root, args); ok {
		code, err := runTeamCommand(args[0], c, args[1:], stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "aw: %v\n", err)
		}
		return code
	}
	root.SetArgs(args)

	cmd, err := root.ExecuteC()
	var status *exitStatus
	if errors.As(err, &status) {
		return status.code // the command's own status; it has printed what it had to say
	}
	var queued *queuedError
	if errors.As(err, &queued) {
		fmt.Fprintln(stderr, "aw: "+queued.message)
		return ExitQueued
	}
	code := exitCode(err)
	if err != nil {
		fmt.Fprintf(stderr, "aw: %v\n", err)
		var silent *silentError
		if code == ExitError && wantsJSON(cmd) && !errors.As(err, &silent) {
			_ = writeJSON(stdout, newJSONError(err))
		}
	}
	if code == ExitUsage {
		where := root
		if cmd != nil {
			where = cmd
		}
		fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", where.CommandPath())
	}
	return code
}

// exitCode classifies the error returned by the command tree.
func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var usage *usageError
	if errors.As(err, &usage) {
		return ExitUsage
	}
	var failed *commandError
	if errors.As(err, &failed) {
		return ExitError
	}
	// Every error that does not come from a command's own RunE comes from cobra itself: an unknown
	// command, an unknown flag, or arguments that do not match. Those are usage errors.
	return ExitUsage
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "aw",
		Short:         "Agent Workspace: parallel lanes for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		// aw alone is the dashboard. The root must be runnable for cobra to apply Args, which is what
		// turns an unknown command into a usage error.
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			return dashboard(cmd, asJSON)
		}),
	}
	root.Flags().Bool("json", false, "print the dashboard as JSON")
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(newVersionCmd(), newConfigCmd(), newInitCmd(), newSetupCmd(), newBackgroundSetupCmd(), newSnapshotCmd(), newRestoreCmd(), newSnapshotsCmd(), newDoctorCmd(), newWhoamiCmd(), newAttachCmd(),
		newContainersCmd(), newKeepCmd(), newSweepCmd(), newDiscardCmd(), newParkCmd(false), newParkCmd(true), newFinishCmd(), newRunCmd(), newNewCmd(), newRecapCmd(), newPairCmd(), newResumeCmd(), newOpenCmd(), newServeCmd(), newStopCmd(), newQueueCmd())
	root.PersistentPreRunE = refuseAgent
	return root
}

// commandError marks an error returned by a command after its arguments were accepted, so that
// Main can tell it apart from a usage error.
type commandError struct{ err error }

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

// run wraps a command's implementation so that its errors exit with ExitError.
func run(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return &commandError{err}
		}
		return nil
	}
}
