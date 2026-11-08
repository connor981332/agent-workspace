// Command awtest-hook sends aw's hook one tool call, as a harness would, and passes on its exit
// status, stdout and stderr. The payload has the harness's recorded shape.
//
//	awtest-hook [-harness claude|codex|grok] [-session id] [-cwd dir] <tool> <argument>
//
// tool is bash (the argument is the command), read, write, edit or list (a path; list is Grok's), or
// patch (a Codex apply_patch body, or @file to read it from a file). The working directory defaults
// to the current one.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	harness := flag.String("harness", "claude", "the harness whose payload to send")
	session := flag.String("session", "awtest-session", "the harness's session ID")
	cwd := flag.String("cwd", "", "the session's working directory (default: the current one)")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: awtest-hook [-harness h] [-session id] [-cwd dir] bash|read|write|edit|patch <argument>")
		os.Exit(64)
	}
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	tool, arg := flag.Arg(0), flag.Arg(1)
	if rest, ok := strings.CutPrefix(arg, "@"); ok && tool == "patch" {
		data, err := os.ReadFile(rest)
		if err != nil {
			fail("%v", err)
		}
		arg = string(data)
	}
	claude := map[string][2]string{"bash": {"Bash", "command"}, "read": {"Read", "file_path"}, "write": {"Write", "file_path"}, "edit": {"Edit", "file_path"}, "patch": {"apply_patch", "command"}}
	grok := map[string][2]string{"bash": {"run_terminal_command", "command"}, "read": {"read_file", "target_file"}, "write": {"search_replace", "file_path"}, "edit": {"search_replace", "file_path"}, "list": {"list_dir", "target_directory"}}
	var payload map[string]any
	switch *harness {
	case "claude", "codex":
		t, ok := claude[tool]
		if !ok {
			fail("unknown tool %q", tool)
		}
		payload = map[string]any{"session_id": *session, "cwd": *cwd, "hook_event_name": "PreToolUse",
			"transcript_path": "/dev/null", "permission_mode": "default", "tool_name": t[0], "tool_input": map[string]any{t[1]: arg}}
	case "grok":
		t, ok := grok[tool]
		if !ok {
			fail("unknown tool %q", tool)
		}
		payload = map[string]any{"hookEventName": "pre_tool_use", "hook_event_name": "PreToolUse", "sessionId": *session, "cwd": *cwd,
			"workspaceRoot": *cwd, "permissionMode": "default", "toolName": t[0], "toolInput": map[string]any{t[1]: arg},
			"toolUseId": "t1", "toolInputTruncated": false}
	default:
		fail("unknown harness %q", *harness)
	}
	data, _ := json.Marshal(payload)
	cmd := exec.Command("aw", "hook", *harness, "pre-tool-use")
	cmd.Dir = *cwd
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "awtest-hook: "+format+"\n", args...)
	os.Exit(64)
}
