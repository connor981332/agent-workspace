package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connor981332/agent-workspace/internal/policy"
)

func fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", dir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestClaudeRecorded normalizes every payload Claude Code 2.1.319 sent in the recording.
func TestClaudeRecorded(t *testing.T) {
	const dir = "claude-2.1.319"
	for name, want := range map[string]struct {
		event string
		tool  policy.Tool
		paths []string
		cmd   string
	}{
		"session-start":       {event: SessionStart},
		"user-prompt-submit":  {event: UserPrompt},
		"pre-tool-use-read":   {event: PreToolUse, tool: policy.Read, paths: []string{"/WORKSPACE/notes.txt"}},
		"pre-tool-use-write":  {event: PreToolUse, tool: policy.Write, paths: []string{"/WORKSPACE/out.txt"}},
		"pre-tool-use-edit":   {event: PreToolUse, tool: policy.Write, paths: []string{"/WORKSPACE/out.txt"}},
		"pre-tool-use-bash":   {event: PreToolUse, tool: policy.Shell, cmd: "ls -la && git status --short"},
		"pre-tool-use-grep":   {event: PreToolUse, tool: policy.Read, paths: []string{"/WORKSPACE"}},
		"pre-tool-use-glob":   {event: PreToolUse, tool: policy.Read, paths: []string{"/WORKSPACE"}},
		"post-tool-use-write": {event: PostToolUse, tool: policy.Write, paths: []string{"/WORKSPACE/out.txt"}},
		"stop":                {event: Stop},
		"session-end":         {event: SessionEnd},
	} {
		p, a, err := Normalize("claude", "", fixture(t, dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if a.name != "claude" || p.Harness != "claude" || p.Session != "SESSION" || p.Cwd != "/WORKSPACE" || p.Event != want.event {
			t.Errorf("%s: %+v", name, p)
		}
		if want.event == PreToolUse || want.event == PostToolUse {
			if p.Call.Tool != want.tool || !slices.Equal(p.Call.Paths, want.paths) || p.Call.Command != want.cmd || p.Call.Cwd != "/WORKSPACE" {
				t.Errorf("%s: call %+v, want %+v", name, p.Call, want)
			}
		}
		if want.event == UserPrompt && !strings.HasPrefix(p.Prompt, "Do exactly these steps") {
			t.Errorf("%s: prompt %q", name, p.Prompt)
		}
	}
	// The event the hook is installed for must be the payload's.
	if _, _, err := Normalize("claude", SessionStart, fixture(t, dir, "pre-tool-use-bash")); err == nil {
		t.Errorf("a PreToolUse payload accepted by a session-start hook")
	}
}

func TestUnknownPayloadsDeny(t *testing.T) {
	for _, raw := range []string{
		``, `[]`, `null`, `{}`, `{"cwd":"/w"}`,
		`{"session_id":"s","cwd":"/w","hook_event_name":"Mystery"}`,
		`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{}}`,
		`{"session_id":"s","cwd":"/w","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{}}`,
		`{"session_id":"s","cwd":"/w","hook_event_name":"PreToolUse","tool_input":{}}`,
	} {
		var stderr bytes.Buffer
		if code := Hook([]string{"claude", "pre-tool-use"}, "test", strings.NewReader(raw), &bytes.Buffer{}, &stderr); code != Deny {
			t.Errorf("%q: exit %d, want a deny", raw, code)
		}
	}
}

func TestToolMapping(t *testing.T) {
	in := func(tool string, input map[string]any) []byte {
		data, _ := json.Marshal(map[string]any{"session_id": "s", "cwd": "/w", "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input})
		return data
	}
	for _, tc := range []struct {
		harness, tool string
		input         map[string]any
		want          policy.Tool
		paths         []string
	}{
		{"claude", "MultiEdit", map[string]any{"file_path": "/w/a", "edits": []any{map[string]any{"file_path": "/w/b"}}}, policy.Write, []string{"/w/a", "/w/b"}},
		{"claude", "NotebookEdit", map[string]any{"notebook_path": "/w/n.ipynb"}, policy.Write, []string{"/w/n.ipynb"}},
		{"claude", "Glob", map[string]any{"pattern": "/etc/**/*.conf"}, policy.Read, []string{"/etc/"}},
		{"claude", "WebFetch", map[string]any{"url": "https://example.com"}, policy.Other, nil},
		{"claude", "mcp__server__tool", map[string]any{"q": 1}, policy.Other, nil},
		{"claude", "SomeNewTool", map[string]any{"path": "/w/x"}, policy.Write, []string{"/w/x"}},
		{"codex", "apply_patch", map[string]any{"command": "*** Begin Patch\n*** Update File: a.go\n*** Add File: b/c.go\n*** Delete File: d\n*** Update File: e\n*** Move to: f\n*** End Patch"}, policy.Write, []string{"a.go", "b/c.go", "d", "e", "f"}},
	} {
		p, _, err := Normalize(tc.harness, PreToolUse, in(tc.tool, tc.input))
		if err != nil {
			t.Errorf("%s %s: %v", tc.harness, tc.tool, err)
			continue
		}
		if p.Call.Tool != tc.want || !slices.Equal(p.Call.Paths, tc.paths) {
			t.Errorf("%s %s: %+v", tc.harness, tc.tool, p.Call)
		}
	}
}

// TestGrokShape covers Grok's documented payload, which it sends even to a Claude Code hook.
func TestGrokShape(t *testing.T) {
	raw := `{"hookEventName":"pre_tool_use","hook_event_name":"PreToolUse","sessionId":"abc","cwd":"/w","workspaceRoot":"/w",
		"permissionMode":"default","toolName":"run_terminal_command","toolInput":{"command":"npm test"},"toolUseId":"t","toolInputTruncated":false}`
	p, a, err := Normalize("claude", PreToolUse, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if a.name != "grok" || p.Harness != "grok" || p.Session != "abc" || p.Call.Tool != policy.Shell || p.Call.Command != "npm test" {
		t.Errorf("%+v", p)
	}
	truncated := strings.Replace(raw, `"toolInputTruncated":false`, `"toolInputTruncated":true`, 1)
	if _, _, err := Normalize("grok", PreToolUse, []byte(truncated)); err == nil {
		t.Errorf("a truncated tool input was accepted")
	}
	var out, errOut bytes.Buffer
	if code := replyGrok(p, policy.Decision{Verdict: policy.Deny, Message: "no"}, &out, &errOut); code != Deny || !strings.Contains(out.String(), `"decision":"deny"`) {
		t.Errorf("a Grok deny: %d %s", code, out.String())
	}
}

func TestClaudeReplies(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := replyClaude(Payload{}, policy.Decision{Verdict: policy.Deny, Message: "outside the lane"}, &out, &errOut); code != Deny || !strings.Contains(errOut.String(), "outside the lane") {
		t.Errorf("deny: %d %q", code, errOut.String())
	}
	out.Reset()
	if code := replyClaude(Payload{}, policy.Decision{Verdict: policy.Ask, Message: "push?"}, &out, &errOut); code != Allow || !strings.Contains(out.String(), `"permissionDecision":"ask"`) {
		t.Errorf("ask: %d %q", code, out.String())
	}
	out.Reset()
	if code := replyClaude(Payload{}, policy.Decision{Verdict: policy.Allow}, &out, &errOut); code != Allow || out.Len() != 0 {
		t.Errorf("allow: %d %q", code, out.String())
	}
}
