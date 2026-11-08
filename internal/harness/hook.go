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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/platform"
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

	if p.Event != PreToolUse && p.Event != SessionStart {
		return Allow // no tool call to decide
	}
	scope, err := resolveScope(p, version, t)
	if err != nil {
		return refuse(err)
	}
	if scope == nil {
		return Allow // not in a workspace: nothing here is aw's to protect
	}
	if p.Event == SessionStart {
		fmt.Fprintln(stdout, startContext(*scope))
		return Allow
	}
	scope.AskSupported = a.ask
	d := policy.Decide(*scope, p.Call)
	t.Mark("decide")
	return a.reply(p, d, stdout, stderr)
}

// resolveScope builds the policy's scope from the workspace around the call's directory: its
// configuration (from the cache when it is current) and its state, read-only.
//
// A session with a current attachment works in its lane, wherever it runs from. Without one, the
// directory decides: a call in a lane root is confined to that lane, and one in the owner checkout
// is unattached (or a pair session, when the individual layer says owner_sessions = "pair").
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

	n, attached := 0, false
	if s != nil {
		var stale bool
		if n, stale, err = attachment(s, stateDir, p); err != nil {
			return nil, err
		}
		attached = n > 0
		if stale && ws.LaneRoot != "" {
			return nil, fmt.Errorf("the lane at %s has moved on to other work since this session attached → go back to the owner checkout, and run bin/aw attach \"<what you are working on>\"", ws.LaneRoot)
		}
	}
	if !attached {
		// No current attachment: the working directory decides.
		if ws.LaneRoot == "" {
			return scope, nil
		}
		if n, err = strconv.Atoi(filepath.Base(ws.LaneRoot)); err != nil || s == nil {
			return nil, fmt.Errorf("%s is a lane root with no record in the state → run aw doctor", ws.LaneRoot)
		}
	}
	root, dirName := ws.LaneRoot, workspace.LaneDir(n, cfg.Lanes.Max)
	if attached || root == "" {
		root = filepath.Join(ws.Owner, ".worktrees", dirName)
	}
	lane, err := state.LaneByNumber(s.DB(), n)
	if err != nil {
		return nil, fmt.Errorf("lane %s has no record in the state (%v) → run aw doctor", dirName, err)
	}
	repos, err := state.LaneRepos(s.DB(), n)
	if err != nil {
		return nil, err
	}
	scope.Role = policy.Lane
	scope.LaneRoot = root
	scope.LaneName = lane.Name
	scope.LaneDirs = []string{shell.Resolve(root), shell.Resolve(filepath.Join(stateDir, "lanes", dirName))}
	for _, r := range repos {
		scope.LaneDirs = append(scope.LaneDirs, shell.Resolve(r.Path))
	}
	return scope, nil
}

// attachment finds the lane a hook call's session is attached to. It returns 0 when there is none:
// no record, a record that has ended, or one whose process has gone. stale means the session's lane
// has since moved on, or been recycled or destroyed (architecture.md, step 2: a stale session cannot
// act on it).
//
// On a session's first hook call it records the harness's own session ID (aw new only knows the one
// it made up), and the session's process when that was not known; a harness's tool commands may be
// unable to see it, but a hook can. Neither waits: a busy database is tried again on the next call.
func attachment(s *state.Store, stateDir string, p Payload) (lane int, stale bool, err error) {
	rec, err := state.SessionByHarnessID(s.DB(), p.Harness, p.Session)
	adopted := false
	if errors.Is(err, state.ErrNotFound) {
		h, id, ok := strings.Cut(os.Getenv("AW_SESSION"), ":")
		if !ok || h != p.Harness || id == p.Session {
			return 0, false, nil
		}
		adopted = true
		if rec, err = state.SessionByHarnessID(s.DB(), h, id); errors.Is(err, state.ErrNotFound) {
			return 0, false, nil
		}
	}
	if err != nil {
		return 0, false, err
	}
	if rec.Lane == 0 {
		return 0, true, nil // its lane was recycled or destroyed: whatever is there now is not its own
	}
	if rec.State != state.SessionActive {
		return 0, false, nil
	}
	l, err := state.LaneByNumber(s.DB(), rec.Lane)
	if errors.Is(err, state.ErrNotFound) || (err == nil && l.Generation != rec.LaneGeneration) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !platform.Alive(rec.PID, rec.PIDStart) {
		return 0, false, nil
	}
	if adopted || rec.PID == 0 {
		pid, start := 0, ""
		if rec.PID == 0 {
			if pid = platform.HarnessAncestor(os.Getpid(), p.Harness); pid > 0 {
				start, _ = platform.ProcessIdentity(pid)
			}
		}
		if adopted || pid > 0 {
			_ = state.RecordSessionIdentity(stateDir, rec.ID, p.Session, pid, start) // best effort: retried next call
		}
	}
	return rec.Lane, false, nil
}

// startContext tells a new session where it is and how to work there. It changes nothing.
func startContext(s policy.Scope) string {
	switch s.Role {
	case policy.Lane:
		return fmt.Sprintf("You are working in lane %s, at %s. Stay inside it. If your working directory is elsewhere (your original harness cwd), start every shell command with cd %s && and use absolute paths inside the lane for file tools. Run bin/aw whoami if you are unsure.",
			s.LaneName, policy.Quote(s.LaneRoot), policy.Quote(s.LaneRoot))
	case policy.Pair:
		return "This is a pair session in the owner checkout, granted by the human. You may edit here. Destructive Git, unapproved publication and changes to the workspace's own configuration are still refused."
	}
	return `You are in the owner checkout, which belongs to the human. Read-only work needs no attach. Before changing anything, run bin/aw attach "<what you are working on>", then work in the lane root it prints. If the human wants you here instead, they can run aw pair.`
}
