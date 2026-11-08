package cli

// Exit codes are part of aw's interface for scripts and agents (R93). A code, once published, keeps
// its meaning. New codes are added here, with a comment saying when a command returns them.
const (
	// ExitOK means the command did what was asked.
	ExitOK = 0

	// ExitError means the command ran and failed. The message on standard error names the fix.
	ExitError = 1

	// ExitUsage means the command line itself was wrong: an unknown command, an unknown flag, or a
	// missing or extra argument. Nothing was done.
	ExitUsage = 2
)
