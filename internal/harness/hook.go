// Package harness turns a harness's hook call into a decision (architecture.md, "Policy engine"):
// the adapter normalizes the payload, the workspace's configuration and state give the scope, and
// the policy decides. Any error is a deny.
//
// This package is on the hot path: it may not import os/exec or net, and it never writes.
package harness

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/policy"
	"github.com/connor981332/agent-workspace/internal/shell"
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
	code := hook(args, version, stdin, stdout, stderr, t)
	if timing.Debug() {
		t.Report(stderr, "hook")
	}
	return code
}

func hook(args []string, version string, stdin io.Reader, stdout, stderr io.Writer, t *timing.Recorder) int {
	refuse := func(err error) int {
		fmt.Fprintf(stderr, "aw: this call is refused: %v\n", err)
		return Deny
	}
	if len(args) < 2 {
		return refuse(errors.New("usage: aw hook <harness> <event> → check the harness's hook configuration (aw setup writes it)"))
	}
	if !slices.Contains(Harnesses, args[0]) {
		return refuse(fmt.Errorf("%q is not a supported harness (%v) → check the harness's hook configuration", args[0], Harnesses))
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, maxPayload+1))
	if err != nil {
		return refuse(fmt.Errorf("reading the hook payload: %v", err))
	}
	if len(raw) > maxPayload {
		return refuse(errors.New("the hook payload is larger than 4 MB"))
	}
	p, a, err := Normalize(args[0], args[1], raw)
	if err != nil {
		return refuse(err)
	}
	t.Mark("payload")

	if p.Event != PreToolUse {
		// Other events carry no tool call. Session tracking and context come with the session record.
		return Allow
	}
	scope, err := resolveScope(p, version, t)
	if err != nil {
		return refuse(err)
	}
	if scope == nil {
		return Allow // not in a workspace: nothing here is aw's to protect
	}
	scope.AskSupported = a.ask
	d := policy.Decide(*scope, p.Call)
	t.Mark("decide")
	return a.reply(p, d, stdout, stderr)
}

// resolveScope builds the policy's scope from the workspace around the call's directory: its
// configuration (from the cache when it is current) and its state, read-only.
//
// Until the session record is consulted, the role follows the directory: a call in a lane root is
// confined to that lane, and one in the owner checkout is unattached (or a pair session, when the
// individual layer says owner_sessions = "pair").
func resolveScope(p Payload, version string, t *timing.Recorder) (*policy.Scope, error) {
	dir := p.Cwd
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the payload's working directory %q is not absolute", dir)
	}
	ws, err := workspace.Find(dir)
	if err != nil {
		var nf *workspace.NotFoundError
		if errors.As(err, &nf) {
			return nil, nil
		}
		return nil, err
	}
	stateDir, err := ws.StateDir()
	if err != nil {
		return nil, err
	}
	t.Mark("workspace")

	cfg, err := config.ReadCache(filepath.Join(stateDir, "config.cache"), version)
	if err != nil {
		if cfg, err = config.Load(ws.Owner); err != nil {
			return nil, fmt.Errorf("the workspace configuration is invalid, so guards cannot decide: %v", err)
		}
		t.Mark("config")
	} else {
		t.Mark("config-cache")
	}

	scope := &policy.Scope{Role: policy.Unattached, Owner: ws.Owner}
	if cfg.User.OwnerSessions == "pair" {
		scope.Role = policy.Pair
	}
	for name, pr := range cfg.Projects {
		for _, sec := range pr.Secrets {
			scope.Secrets = append(scope.Secrets, filepath.Join(ws.Owner, pr.Directory, sec))
		}
		for sname, sv := range pr.Servers {
			scope.Servers = append(scope.Servers, policy.ServerCommand{Name: name + "." + sname, Words: strings.Fields(sv.Command)})
		}
	}

	s, err := state.Open(stateDir, state.ReadOnly)
	switch {
	case errors.Is(err, state.ErrNoState):
		// Before setup there is no state, and nothing is attached.
	case err != nil:
		return scope, err
	default:
		defer s.Close()
		if s.Newer() {
			return scope, s.ReadOnly()
		}
	}
	t.Mark("state")

	if ws.LaneRoot == "" {
		return scope, nil
	}
	n, err := strconv.Atoi(filepath.Base(ws.LaneRoot))
	if err != nil || s == nil {
		return scope, fmt.Errorf("%s is a lane root with no record in the state → run aw doctor", ws.LaneRoot)
	}
	lane, err := state.LaneByNumber(s.DB(), n)
	if err != nil {
		return scope, fmt.Errorf("lane %s has no record in the state (%v) → run aw doctor", filepath.Base(ws.LaneRoot), err)
	}
	repos, err := state.LaneRepos(s.DB(), n)
	if err != nil {
		return scope, err
	}
	scope.Role = policy.Lane
	scope.LaneRoot = ws.LaneRoot
	scope.LaneName = lane.Name
	scope.LaneDirs = []string{shell.Resolve(ws.LaneRoot), shell.Resolve(filepath.Join(stateDir, "lanes", filepath.Base(ws.LaneRoot)))}
	for _, r := range repos {
		scope.LaneDirs = append(scope.LaneDirs, shell.Resolve(r.Path))
	}
	return scope, nil
}
