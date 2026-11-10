package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// IsHarnessConfig reports whether a path in the workspace is a file aw merges hooks into.
func IsHarnessConfig(rel string) bool {
	for _, c := range harnessConfigs {
		if c.path == rel {
			return true
		}
	}
	return false
}

// OtherHooks lists the hooks in the workspace's harness files that are not aw's, as "<event>: <end
// of the command>". They run alongside aw's, in every lane, and another system's guard can refuse
// everything in a lane it does not recognize.
func OtherHooks(root string) []string {
	const shown = 60
	seen := map[string]bool{}
	var out []string
	for _, c := range harnessConfigs {
		data, err := os.ReadFile(filepath.Join(root, c.path))
		var settings map[string]any
		if err != nil || json.Unmarshal(data, &settings) != nil {
			continue
		}
		events, _ := settings["hooks"].(map[string]any)
		for _, event := range slices.Sorted(maps.Keys(events)) {
			groups, _ := events[event].([]any)
			for _, g := range groups {
				if isAWHook(g) {
					continue
				}
				m, _ := g.(map[string]any)
				hooks, _ := m["hooks"].([]any)
				for _, h := range hooks {
					hm, _ := h.(map[string]any)
					cmd, _ := hm["command"].(string)
					if len(cmd) > shown {
						cmd = "…" + cmd[len(cmd)-shown:]
					}
					if line := event + ": " + cmd; !seen[line] {
						seen[line] = true
						out = append(out, line)
					}
				}
			}
		}
	}
	return out
}

// DropOtherHooks rewrites one harness file with only aw's hooks in it.
func DropOtherHooks(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	events, _ := settings["hooks"].(map[string]any)
	for event, v := range events {
		groups, _ := v.([]any)
		kept := slices.DeleteFunc(slices.Clone(groups), func(g any) bool { return !isAWHook(g) })
		if len(kept) == 0 {
			delete(events, event)
		} else {
			events[event] = kept
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// OpenCodePluginPath is where OpenCode loads aw's plugin from, in the workspace.
const OpenCodePluginPath = ".opencode/plugins/aw.js"

// OpenCodePlugin is OpenCode's guard: OpenCode runs plugins, not hook commands, so this sends each
// tool call to aw hook and throws on anything but success, which cancels the call (measured with
// 2.2.6). The working directory it reports is the workspace or lane root the plugin was loaded from,
// because OpenCode's event does not carry one. For a session started in a subdirectory, a relative
// path is then read from higher up than it really is, which can refuse a path but cannot let one
// out of the lane.
const OpenCodePlugin = `// Managed by aw setup: do not edit. It sends each tool call to aw's guard, and cancels the call
// when the guard refuses or cannot run.
import { spawnSync } from "node:child_process"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..")

export default {
  id: "agent-workspace",
  async setup(ctx) {
    await ctx.tool.hook("execute.before", (event) => {
      const payload = JSON.stringify({
        hook_event_name: "pre-tool-use",
        sessionID: event.sessionID,
        cwd: root,
        tool: event.tool,
        input: event.input,
      })
      const result = spawnSync(join(root, "bin", "aw"), ["hook", "opencode", "pre-tool-use"], {
        cwd: root,
        input: payload,
        encoding: "utf8",
      })
      if (result.error) throw new Error("aw: this call is refused: the guard could not run: " + result.error.message)
      if (result.status !== 0) throw new Error((result.stderr || "aw: this call is refused").trim())
    })
  },
}
`

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
	res, err := installOpenCodePlugin(owner)
	return append(out, res), err
}

func installOpenCodePlugin(owner string) (ConfigResult, error) {
	res := ConfigResult{Path: OpenCodePluginPath}
	path := filepath.Join(owner, filepath.FromSlash(OpenCodePluginPath))
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	if res.Changed = string(old) != OpenCodePlugin; !res.Changed {
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return res, err
	}
	return res, os.WriteFile(path, []byte(OpenCodePlugin), 0o644)
}
