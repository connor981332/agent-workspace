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
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hook", "git-hook":
			os.Exit(hotPath(os.Args[1]))
		}
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}

// hotPath is the stub for the guard and Git hook entry points. The hook stub that returns allow
// arrives in M1 and the real rules in M3. Until then it refuses, because a guard that cannot
// decide must deny.
func hotPath(name string) int {
	fmt.Fprintf(os.Stderr, "aw %s: not implemented yet\n", name)
	return cli.ExitError
}
