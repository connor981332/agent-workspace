// Command aw is the Agent Workspace command line.
//
// main checks for the hot-path commands (hook and git-hook) before anything else and dispatches
// them without building the command tree, so the CLI framework's setup never runs on a guard
// decision (plans/release-candidate.md §4.3, rule 4). Keep this file small: everything it imports
// directly is on the hot path.
package main

import (
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
			os.Exit(harness.GitHook(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		}
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
