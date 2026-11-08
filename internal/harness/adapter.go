package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/policy"
)

// Events, as aw hook names them on its command line and as the adapters normalize them.
const (
	PreToolUse   = "pre-tool-use"
	PostToolUse  = "post-tool-use"
	SessionStart = "session-start"
	UserPrompt   = "user-prompt-submit"
	Stop         = "stop"
	SessionEnd   = "session-end"
)

// Payload is a hook call normalized to the common form (architecture.md, "Policy engine", step 1).
type Payload struct {
	Harness string // the harness that sent it, which may differ from the one the hook was installed for
	Event   string
	Session string // the harness's session ID
	Cwd     string
	Tool    string // the harness's tool name, for messages
	Call    policy.Call
	Prompt  string // UserPromptSubmit
}

// adapter knows one harness's payload and reply formats.
type adapter struct {
	name string
	// ask reports whether the harness can ask the human about a call.
	ask bool
	// normalize reads a payload already decoded into a map.
	normalize func(m map[string]any, event string) (Payload, error)
	// reply writes a decision and returns the exit status.
	reply func(p Payload, d policy.Decision, stdout, stderr io.Writer) int
}

var adapters = map[string]*adapter{
	"claude":   {name: "claude", ask: true, normalize: normalizeClaude, reply: replyClaude},
	"codex":    {name: "codex", ask: false, normalize: normalizeCodex, reply: replyClaude},
	"grok":     {name: "grok", ask: true, normalize: normalizeGrok, reply: replyGrok},
	"opencode": {name: "opencode", ask: false, normalize: normalizeOpenCode, reply: replyPlain},
}

// UnknownPayloadError means a payload aw cannot read: it is denied (R56).
type UnknownPayloadError struct {
	Harness string
	Why     string
}

func (e *UnknownPayloadError) Error() string {
	return fmt.Sprintf("aw does not recognize this %s hook payload (%s) → update aw, or pin a harness version it supports", e.Harness, e.Why)
}

// Normalize reads a hook payload. The adapter is chosen by the payload's shape where it is
// distinctive, since one harness can run another's hook configuration (Grok runs Claude Code's
// .claude/settings.json hooks), and otherwise by the harness the hook was installed for.
func Normalize(installed, event string, raw []byte) (Payload, *adapter, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return Payload{}, nil, &UnknownPayloadError{installed, "it is not a JSON object"}
	}
	a := adapters[installed]
	if a == nil {
		return Payload{}, nil, &UnknownPayloadError{installed, "no adapter"}
	}
	if _, grok := m["hookEventName"]; grok {
		a = adapters["grok"]
	}
	p, err := a.normalize(m, event)
	if err != nil {
		return Payload{}, a, err
	}
	p.Harness = a.name
	return p, a, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func obj(m map[string]any, key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}

// claudeEvents maps hook_event_name to aw's event names.
var claudeEvents = map[string]string{
	"PreToolUse": PreToolUse, "PostToolUse": PostToolUse, "SessionStart": SessionStart,
	"UserPromptSubmit": UserPrompt, "Stop": Stop, "SessionEnd": SessionEnd,
}

// common reads the fields every Claude-shaped payload has.
func common(m map[string]any, harness, event string, sessionKey, eventKey, cwdKey string) (Payload, error) {
	p := Payload{Session: str(m, sessionKey), Cwd: str(m, cwdKey)}
	if p.Session == "" {
		return p, &UnknownPayloadError{harness, "no " + sessionKey}
	}
	if p.Cwd == "" {
		return p, &UnknownPayloadError{harness, "no " + cwdKey}
	}
	name := str(m, eventKey)
	p.Event = claudeEvents[name]
	if p.Event == "" {
		return p, &UnknownPayloadError{harness, fmt.Sprintf("the event %q", name)}
	}
	if event != "" && event != p.Event {
		return p, &UnknownPayloadError{harness, fmt.Sprintf("the hook is installed for %s, but the payload is %s", event, p.Event)}
	}
	return p, nil
}

// Claude Code (measured: 2.1.319, testdata/claude-2.1.319).

var (
	claudeShell = []string{"Bash"}
	claudeRead  = []string{"Read", "Grep", "Glob", "LS", "NotebookRead"}
	claudeWrite = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}
	// Tools with no path: their calls need no path decision.
	claudeOther = []string{"WebFetch", "WebSearch", "Task", "Agent", "TodoWrite", "TodoRead", "ExitPlanMode",
		"EnterPlanMode", "AskUserQuestion", "BashOutput", "KillShell", "KillBash", "SlashCommand", "Skill",
		"ToolSearch", "ListMcpResourcesTool", "ReadMcpResourceTool", "TaskOutput", "TaskStop"}
)

func normalizeClaude(m map[string]any, event string) (Payload, error) {
	p, err := common(m, "claude", event, "session_id", "hook_event_name", "cwd")
	if err != nil {
		return p, err
	}
	switch p.Event {
	case UserPrompt:
		p.Prompt = str(m, "prompt")
	case PreToolUse, PostToolUse:
		p.Tool = str(m, "tool_name")
		p.Call, err = claudeCall(p.Tool, obj(m, "tool_input"), p.Cwd, "claude")
	}
	return p, err
}

// claudeCall normalizes a Claude-shaped tool call (Claude Code, and Codex, which sends that shape).
func claudeCall(tool string, in map[string]any, cwd, harness string) (policy.Call, error) {
	c := policy.Call{Cwd: cwd}
	if tool == "" {
		return c, &UnknownPayloadError{harness, "no tool_name"}
	}
	if in == nil {
		in = map[string]any{}
	}
	paths := filePaths(in)
	switch {
	case slices.Contains(claudeShell, tool):
		c.Tool = policy.Shell
		c.Command = str(in, "command")
		if c.Command == "" {
			return c, &UnknownPayloadError{harness, "a Bash call with no command"}
		}
	case slices.Contains(claudeRead, tool):
		c.Tool = policy.Read
		// A Glob pattern can name a directory itself: check the part before the first wildcard.
		if pat := str(in, "pattern"); tool == "Glob" && (strings.HasPrefix(pat, "/") || strings.HasPrefix(pat, "~")) {
			prefix, _, _ := strings.Cut(pat, "*")
			paths = append(paths, prefix)
		}
		c.Paths = paths
	case slices.Contains(claudeWrite, tool):
		c.Tool = policy.Write
		c.Paths = paths
	case slices.Contains(claudeOther, tool) || strings.HasPrefix(tool, "mcp__"):
		c.Tool = policy.Other
	default:
		// A tool this adapter does not know: any path it names is still checked, as a write.
		c.Tool = policy.Other
		if len(paths) > 0 {
			c.Tool, c.Paths = policy.Write, paths
		}
	}
	return c, nil
}

// filePaths collects the path arguments a tool input may carry.
func filePaths(in map[string]any) []string {
	var out []string
	for _, k := range []string{"file_path", "notebook_path", "path", "target_file", "filePath"} {
		if s := str(in, k); s != "" {
			out = append(out, s)
		}
	}
	if edits, ok := in["edits"].([]any); ok {
		for _, e := range edits {
			if em, ok := e.(map[string]any); ok {
				if s := str(em, "file_path"); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// replyClaude answers in Claude Code's hook contract, which Codex follows too: a deny is exit 2 with
// the reason on stderr; an ask is a permissionDecision on stdout.
func replyClaude(p Payload, d policy.Decision, stdout, stderr io.Writer) int {
	switch d.Verdict {
	case policy.Deny:
		fmt.Fprintf(stderr, "aw: %s\n", d.Message)
		return Deny
	case policy.Ask:
		out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse", "permissionDecision": "ask", "permissionDecisionReason": "aw: " + d.Message,
		}})
		fmt.Fprintln(stdout, string(out))
	}
	return Allow
}

// Codex (not yet recorded: built from C1's adapter, which measured it; testdata/record.sh records
// it). Its payload has Claude Code's shape; it edits files with apply_patch, whose patch names them.

func normalizeCodex(m map[string]any, event string) (Payload, error) {
	p, err := common(m, "codex", event, "session_id", "hook_event_name", "cwd")
	if err != nil {
		return p, err
	}
	switch p.Event {
	case UserPrompt:
		p.Prompt = str(m, "prompt")
	case PreToolUse, PostToolUse:
		p.Tool = str(m, "tool_name")
		in := obj(m, "tool_input")
		if p.Tool == "apply_patch" {
			patch := str(in, "command")
			if patch == "" {
				patch = str(in, "input")
			}
			files := PatchFiles(patch)
			if len(files) == 0 {
				return p, &UnknownPayloadError{"codex", "an apply_patch call that names no file"}
			}
			p.Call = policy.Call{Tool: policy.Write, Cwd: p.Cwd, Paths: files}
			return p, nil
		}
		p.Call, err = claudeCall(p.Tool, in, p.Cwd, "codex")
	}
	return p, err
}

// PatchFiles returns every file a Codex apply_patch body names: added, updated, deleted and moved.
func PatchFiles(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				out = append(out, strings.TrimSpace(rest))
			}
		}
	}
	return out
}

// Grok (not yet recorded: built from its hook documentation and C1's adapter). Its payload has
// camelCase fields and its own tool names.

var (
	grokShell = []string{"run_terminal_command", "Shell"}
	grokRead  = []string{"read_file", "grep", "list_dir", "Read", "Grep", "List"}
	grokWrite = []string{"search_replace", "write", "hashline_edit", "StrReplace", "Write", "EditNotebook"}
	grokOther = []string{"web_search", "web_fetch", "Fetch", "spawn_subagent", "Task", "AskQuestion", "AwaitShell", "todo_write", "use_tool"}
)

func normalizeGrok(m map[string]any, event string) (Payload, error) {
	p, err := common(m, "grok", event, "sessionId", "hook_event_name", "cwd")
	if err != nil {
		return p, err
	}
	switch p.Event {
	case UserPrompt:
		p.Prompt = str(m, "prompt")
	case PreToolUse, PostToolUse:
		p.Tool = str(m, "toolName")
		in := obj(m, "toolInput")
		if in == nil {
			in = map[string]any{}
		}
		if b, _ := m["toolInputTruncated"].(bool); b {
			return p, &UnknownPayloadError{"grok", "the tool input was truncated, so it cannot be checked in full"}
		}
		c := policy.Call{Cwd: p.Cwd}
		paths := filePaths(in)
		switch {
		case slices.Contains(grokShell, p.Tool):
			c.Tool, c.Command = policy.Shell, str(in, "command")
			if c.Command == "" {
				return p, &UnknownPayloadError{"grok", "a shell call with no command"}
			}
		case slices.Contains(grokRead, p.Tool):
			c.Tool, c.Paths = policy.Read, paths
		case slices.Contains(grokWrite, p.Tool):
			c.Tool, c.Paths = policy.Write, paths
			if len(paths) == 0 {
				return p, &UnknownPayloadError{"grok", "a " + p.Tool + " call that names no file"}
			}
		case slices.Contains(grokOther, p.Tool) || strings.HasPrefix(p.Tool, "mcp"):
			c.Tool = policy.Other
		default:
			c.Tool = policy.Other
			if len(paths) > 0 {
				c.Tool, c.Paths = policy.Write, paths
			}
		}
		p.Call = c
	}
	return p, nil
}

// replyGrok answers in Grok's contract: a decision on stdout, and exit 2 for a deny, which an
// allow reply cannot lift.
func replyGrok(p Payload, d policy.Decision, stdout, stderr io.Writer) int {
	switch d.Verdict {
	case policy.Deny:
		out, _ := json.Marshal(map[string]string{"decision": "deny", "reason": "aw: " + d.Message})
		fmt.Fprintln(stdout, string(out))
		fmt.Fprintf(stderr, "aw: %s\n", d.Message)
		return Deny
	case policy.Ask:
		out, _ := json.Marshal(map[string]string{"decision": "ask", "reason": "aw: " + d.Message})
		fmt.Fprintln(stdout, string(out))
	}
	return Allow
}

// OpenCode (M3, later: its managed plugin calls aw hook opencode with this shape).

func normalizeOpenCode(m map[string]any, event string) (Payload, error) {
	return Payload{}, &UnknownPayloadError{"opencode", "the OpenCode plugin is not built yet"}
}

func replyPlain(p Payload, d policy.Decision, stdout, stderr io.Writer) int {
	if d.Verdict != policy.Allow {
		fmt.Fprintf(stderr, "aw: %s\n", d.Message)
		return Deny
	}
	return Allow
}
