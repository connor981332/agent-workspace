// Package harness turns a harness's hook call into a decision. Until M3 it is the stub that M1's
// budget is enforced against (plans/release-candidate.md, M1): it decodes the payload, loads the
// compiled configuration and opens the state read-only, then allows. Any error is a deny.
//
// This package is on the hot path: it may not import os/exec or net, and it never writes.
package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/timing"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Harnesses are the tier-1 harnesses, whose hooks aw enforces.
var Harnesses = []string{"claude", "codex", "grok", "opencode"}

// Exit codes of a hook call. Harnesses and Git read 2 as a refusal.
const (
	Allow = 0
	Deny  = 2
)

const maxPayload = 4 << 20

// Hook handles aw hook <harness> <event>. version is this binary's, which keys the config cache.
func Hook(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	t := timing.Start()
	err := decide(args, version, stdin, t)
	if timing.Debug() {
		t.Report(stderr, "hook")
	}
	if err != nil {
		fmt.Fprintf(stderr, "aw: this call is refused: %v\n", err)
		return Deny
	}
	return Allow
}

func decide(args []string, version string, stdin io.Reader, t *timing.Recorder) error {
	if len(args) < 2 {
		return errors.New("usage: aw hook <harness> <event> → check the harness's hook configuration (aw setup writes it)")
	}
	if !slices.Contains(Harnesses, args[0]) {
		return fmt.Errorf("%q is not a supported harness (%v) → check the harness's hook configuration", args[0], Harnesses)
	}

	raw, err := io.ReadAll(io.LimitReader(stdin, maxPayload+1))
	if err != nil {
		return fmt.Errorf("reading the hook payload: %v", err)
	}
	if len(raw) > maxPayload {
		return errors.New("the hook payload is larger than 4 MB")
	}
	var payload struct {
		Cwd string `json:"cwd"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("the %s payload is not valid JSON (%v) → update aw, or pin a harness version it supports", args[0], err)
		}
	}
	t.Mark("payload")

	dir := payload.Cwd
	if dir == "" || !filepath.IsAbs(dir) {
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	ws, err := workspace.Find(dir)
	if err != nil {
		return err
	}
	stateDir, err := ws.StateDir()
	if err != nil {
		return err
	}
	t.Mark("workspace")

	if _, err := config.ReadCache(filepath.Join(stateDir, "config.cache"), version); err != nil {
		if _, err := config.Load(ws.Owner); err != nil {
			return fmt.Errorf("the workspace configuration is invalid, so guards cannot decide: %v", err)
		}
		t.Mark("config")
	} else {
		t.Mark("config-cache")
	}

	s, err := state.Open(stateDir, state.ReadOnly)
	switch {
	case errors.Is(err, state.ErrNoState):
		// Before setup there is no state, and nothing is attached.
	case err != nil:
		return err
	default:
		defer s.Close()
		if s.Newer() {
			return s.ReadOnly()
		}
	}
	t.Mark("state")
	return nil
}
