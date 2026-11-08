package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookEvents are the events aw needs, as harnesses name them and as aw hook names them.
var hookEvents = []struct{ event, name string }{
	{"SessionStart", "session-start"}, {"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"}, {"SessionEnd", "session-end"},
}

// harnessConfigs are the files aw merges its hooks into. Grok also runs Claude Code's file, and
// tells its payloads apart, so it needs none of its own.
var harnessConfigs = []struct{ harness, path string }{
	{"claude", ".claude/settings.json"}, {"codex", ".codex/hooks.json"},
}

// hookCommand finds bin/aw from the project directory with shell builtins only, and turns anything
// but success into exit 2, which harnesses read as a refusal: a hook that cannot start must not
// fail open.
func hookCommand(harness, event string) string {
	return fmt.Sprintf(`d="${CLAUDE_PROJECT_DIR:-$PWD}"; while [ -n "$d" ] && [ ! -x "$d/bin/aw" ]; do d="${d%%/*}"; done; "$d/bin/aw" hook %s %s || exit 2`, harness, event)
}

func isAWHook(entry any) bool {
	group, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, _ := group["hooks"].([]any)
	for _, h := range hooks {
		if m, ok := h.(map[string]any); ok {
			if c, _ := m["command"].(string); strings.Contains(c, `/bin/aw" hook `) {
				return true
			}
		}
	}
	return false
}

// ConfigResult says what aw setup did to one harness's configuration.
type ConfigResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

// InstallHarnessConfig merges aw's hooks into each harness's project configuration: other settings
// and other tools' hooks are kept, and aw's own entries are replaced. It is safe to rerun.
func InstallHarnessConfig(owner string) ([]ConfigResult, error) {
	var out []ConfigResult
	for _, c := range harnessConfigs {
		path := filepath.Join(owner, filepath.FromSlash(c.path))
		settings := map[string]any{}
		old, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(old, &settings); err != nil {
				return out, fmt.Errorf("%s is not valid JSON (%v), so aw left it alone → fix it, then run aw setup again", c.path, err)
			}
		case !os.IsNotExist(err):
			return out, err
		}
		hooks, _ := settings["hooks"].(map[string]any)
		if hooks == nil {
			hooks = map[string]any{}
		}
		for _, e := range hookEvents {
			var kept []any
			existing, _ := hooks[e.event].([]any)
			for _, entry := range existing {
				if !isAWHook(entry) {
					kept = append(kept, entry)
				}
			}
			group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(c.harness, e.name)}}}
			if c.harness == "claude" && e.event == "PreToolUse" {
				group["matcher"] = "*"
			}
			hooks[e.event] = append(kept, group)
		}
		settings["hooks"] = hooks
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // people read this file: && stays &&
		enc.SetIndent("", "  ")
		if err := enc.Encode(settings); err != nil {
			return out, err
		}
		data := buf.Bytes()
		res := ConfigResult{Path: c.path, Changed: string(old) != string(data)}
		if res.Changed {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return out, err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return out, err
			}
		}
		out = append(out, res)
	}
	return out, nil
}
