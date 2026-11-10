package sandbox

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// HarnessFiles are the files aw merges its hooks into. Grok also runs Claude Code's file, and tells
// its payloads apart, so it needs none of its own.
var HarnessFiles = []struct{ Harness, Path string }{
	{"claude", ".claude/settings.json"}, {"codex", ".codex/hooks.json"},
}

// IsAWHook reports whether one hook group in a harness file is aw's.
func IsAWHook(entry any) bool {
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

// HarnessConfigs are the files aw merges hooks into, relative to the workspace.
func HarnessConfigs() []string {
	out := make([]string, len(HarnessFiles))
	for i, c := range HarnessFiles {
		out[i] = c.Path
	}
	return out
}

// otherHooksIn lists the hooks in one harness file that are not aw's, as "<event>: <end of the command>".
func otherHooksIn(path string) []string {
	const shown = 60
	data, err := os.ReadFile(path)
	var settings map[string]any
	if err != nil || json.Unmarshal(data, &settings) != nil {
		return nil
	}
	var out []string
	events, _ := settings["hooks"].(map[string]any)
	for _, event := range slices.Sorted(maps.Keys(events)) {
		groups, _ := events[event].([]any)
		for _, g := range groups {
			if IsAWHook(g) {
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
				out = append(out, event+": "+cmd)
			}
		}
	}
	return out
}

// HasAWHooks reports whether aw's own hooks are installed in a workspace's harness files.
func HasAWHooks(root string) bool {
	for _, c := range HarnessFiles {
		data, err := os.ReadFile(filepath.Join(root, c.Path))
		if err == nil && strings.Contains(string(data), `/bin/aw\" hook `) {
			return true
		}
	}
	return false
}

// HasOtherHooks reports whether one harness file holds hooks that are not aw's.
func HasOtherHooks(path string) bool { return len(otherHooksIn(path)) > 0 }

// OtherHooks lists the hooks in the workspace's harness files that are not aw's. They run alongside
// aw's, in every lane, and another system's guard can refuse everything in a lane it does not recognize.
func OtherHooks(root string) []string {
	var out []string
	for _, c := range HarnessFiles {
		for _, line := range otherHooksIn(filepath.Join(root, c.Path)) {
			if !slices.Contains(out, line) {
				out = append(out, line)
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
		kept := slices.DeleteFunc(slices.Clone(groups), func(g any) bool { return !IsAWHook(g) })
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
