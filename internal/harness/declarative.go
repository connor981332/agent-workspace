package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/policy"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// declarativeAdapter is a harness the team described in workspace.toml (architecture.md, "Harness
// adapters"), or nil. A built-in adapter is never replaced by one.
func declarativeAdapter(name, version string) *adapter {
	if slices.Contains(Harnesses, name) {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return nil
	}
	dir, err := ws.StateDir()
	if err != nil {
		return nil
	}
	cfg, err := config.ReadCache(filepath.Join(dir, "config.cache"), version)
	if err != nil {
		if cfg, err = config.Load(ws.Owner); err != nil {
			return nil
		}
	}
	h := cfg.Harnesses[name]
	if h == nil {
		return nil
	}
	return &adapter{name: name, ask: false, reply: replyPlain, normalize: func(m map[string]any, event string) (Payload, error) {
		return normalizeDeclared(name, h, m, event)
	}}
}

func normalizeWith(a *adapter, event string, raw []byte) (Payload, *adapter, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return Payload{}, a, &UnknownPayloadError{a.name, "it is not a JSON object"}
	}
	p, err := a.normalize(m, event)
	p.Harness = a.name
	return p, a, err
}

func normalizeDeclared(name string, h *config.Harness, m map[string]any, event string) (Payload, error) {
	p := Payload{Session: str(m, h.Session), Cwd: str(m, h.Cwd)}
	if p.Session == "" || p.Cwd == "" {
		return p, &UnknownPayloadError{name, "no session or working directory"}
	}
	raw := str(m, h.Event)
	p.Event = h.Events[raw]
	if p.Event == "" {
		return p, &UnknownPayloadError{name, fmt.Sprintf("the event %q", raw)}
	}
	if event != "" && event != p.Event {
		return p, &UnknownPayloadError{name, fmt.Sprintf("the hook is installed for %s, but the payload is %s", event, p.Event)}
	}
	if p.Event != PreToolUse {
		return p, nil
	}
	p.Tool = str(m, h.Tool)
	input := obj(m, h.Input)
	p.Call.Cwd = p.Cwd
	switch {
	case slices.Contains(h.ShellTools, p.Tool):
		p.Call.Tool, p.Call.Command = policy.Shell, str(input, h.Command)
		if p.Call.Command == "" {
			return p, &UnknownPayloadError{name, "a shell tool with no command"}
		}
	case slices.Contains(h.ReadTools, p.Tool):
		p.Call.Tool, p.Call.Paths = policy.Read, []string{str(input, h.Path)}
	case slices.Contains(h.WriteTools, p.Tool):
		p.Call.Tool, p.Call.Paths = policy.Write, []string{str(input, h.Path)}
	case slices.Contains(h.OtherTools, p.Tool):
		p.Call.Tool = policy.Other
	default:
		return p, &UnknownPayloadError{name, fmt.Sprintf("the tool %q", p.Tool)}
	}
	return p, nil
}
