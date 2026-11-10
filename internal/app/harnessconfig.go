package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/connor981332/agent-workspace/internal/sandbox"
)

// hookEvents are the events aw needs, as harnesses name them and as aw hook names them.
var hookEvents = []struct{ event, name string }{
	{"SessionStart", "session-start"}, {"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"}, {"SessionEnd", "session-end"},
}

// hookCommand finds bin/aw from the project directory with shell builtins only, and turns anything
// but success into exit 2, which harnesses read as a refusal: a hook that cannot start must not
// fail open.
func hookCommand(harness, event string) string {
	return fmt.Sprintf(`d="${CLAUDE_PROJECT_DIR:-$PWD}"; while [ -n "$d" ] && [ ! -x "$d/bin/aw" ]; do d="${d%%/*}"; done; "$d/bin/aw" hook %s %s || exit 2`, harness, event)
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
	for _, c := range sandbox.HarnessFiles {
		path := filepath.Join(owner, filepath.FromSlash(c.Path))
		settings := map[string]any{}
		old, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(old, &settings); err != nil {
				return out, fmt.Errorf("%s is not valid JSON (%v), so aw left it alone → fix it, then run aw setup again", c.Path, err)
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
				if !sandbox.IsAWHook(entry) {
					kept = append(kept, entry)
				}
			}
			group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(c.Harness, e.name)}}}
			if c.Harness == "claude" && e.event == "PreToolUse" {
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
		res := ConfigResult{Path: c.Path, Changed: string(old) != string(data)}
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
