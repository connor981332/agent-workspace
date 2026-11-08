// Command aw is the Agent Workspace command line.
//
// main checks for the hot-path commands (hook and git-hook) before anything else and dispatches
// them without building the command tree, so the CLI framework's setup never runs on a guard
// decision (plans/release-candidate.md §4.3, rule 4). Keep this file small: everything it imports
// directly is on the hot path.
package main

import (
	"fmt"
	"os"

	"github.com/connor981332/agent-workspace/internal/cli"
	"github.com/connor981332/agent-workspace/internal/harness"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hook":
			os.Exit(harness.Hook(os.Args[2:], cli.FrameworkVersion(), os.Stdin, os.Stdout, os.Stderr))
		case "git-hook":
			os.Exit(gitHook())
		}
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}

// gitHook is the stub for the Git hooks dispatcher, which arrives in M3. Nothing installs it
// before then; if something calls it, it refuses, because a guard that cannot decide must deny.
func gitHook() int {
	fmt.Fprintln(os.Stderr, "aw git-hook: not implemented yet, so this ref update is refused")
	return harness.Deny
}
