// Command floor prints a fixed decision: the cost of starting any Go process.
package main

import "os"

func main() {
	os.Stdout.WriteString(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}` + "\n")
}
