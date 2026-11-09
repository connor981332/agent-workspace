// Package policy decides one tool call: allow, deny with a message that names the fix, or ask
// (architecture.md, "Policy engine"). It is pure: it reads the paths it resolves and nothing else,
// runs no process and writes nothing. Any error is a deny.
//
// This package is on the hot path: it may not import os/exec or net.
package policy

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Tool is the kind of a tool call, as an adapter normalizes it.
type Tool int

const (
	Read  Tool = iota // reads files: Read, Grep, Glob, a viewer
	Write             // writes files: Write, Edit, a patch
	Shell             // runs a shell command
	Other             // anything else, such as a web fetch: no path decision
)

// Call is one normalized tool call.
type Call struct {
	Tool    Tool
	Cwd     string   // the session's working directory, absolute
	Paths   []string // the files a Read or Write names; relative ones are relative to Cwd
	Command string   // the shell command, for Shell
}

// Role is what the session is attached to.
type Role int

const (
	// Unattached: a session in the owner checkout with no lane. It may read; its first write is
	// refused with the attach and pair instructions (lazy claiming).
	Unattached Role = iota
	// Lane: a session attached to a lane. It is confined to the lane.
	Lane
	// Pair: a pair session in the owner checkout, granted by the human (R60, R61).
	Pair
)

// Scope is everything a decision depends on, resolved by the caller from the configuration and
// the state.
type Scope struct {
	Role  Role
	Owner string // the owner checkout, absolute
	// Session is the session as <harness>:<id>, so a refusal can tell the human how to grant it.
	Session string
	// Namespace is the workspace's, which names the individual layer's files.
	Namespace string
	// For a lane: its root, the physical directories it may use (the root, its project worktrees,
	// its private state directory), and its name, for messages.
	LaneRoot string
	LaneDirs []string
	LaneName string
	Home     string
	// Secrets are the secret files' absolute paths: the owner's files and each lane's read-only
	// links to them.
	Secrets []string
	// Servers are the configured server commands, which only aw serve may start.
	Servers []ServerCommand
	// MuxProgram is the program of a configured multiplexer template, which a lane may not use.
	MuxProgram string
	// Guards are the team's own guards, in name order.
	Guards []TeamGuard
	// RoleName is the lane's team role, and RoleInstructions the file its sessions receive.
	RoleName, RoleInstructions string
	// RoleAllow and RoleDeny are the capabilities of the lane's team role (R65).
	RoleAllow, RoleDeny []string
	// Publication are the pre-authorized actions, and ProjectPublication those of each project's
	// worktree in this lane, by physical directory (R72).
	Publication        []string
	ProjectPublication map[string][]string
	// LaneBranches are the branches a pre-authorized push from this lane may touch.
	LaneBranches []string
	// Delegated is whether a maintenance delegation applies to this owner-checkout session, and
	// DelegatedCommands the team's commands it covers (R64).
	Delegated         bool
	DelegatedCommands []string
	// AskSupported is whether the harness can ask the human; if not, an ask is a deny.
	AskSupported bool
}

// ServerCommand is a configured server's command, as words.
type ServerCommand struct {
	Name  string // <project>.<server>
	Words []string
}

// Verdict is the decision.
type Verdict int

const (
	Allow Verdict = iota
	Deny
	Ask
)

func (v Verdict) String() string {
	return [...]string{"allow", "deny", "ask"}[v]
}

// Decision is a verdict with the rule that made it and a message that names the fix.
type Decision struct {
	Verdict Verdict
	Rule    string
	Message string
}

func allow() Decision { return Decision{Verdict: Allow} }

func deny(rule, format string, args ...any) Decision {
	return Decision{Verdict: Deny, Rule: rule, Message: fmt.Sprintf(format, args...)}
}

func ask(rule, format string, args ...any) Decision {
	return Decision{Verdict: Ask, Rule: rule, Message: fmt.Sprintf(format, args...)}
}

// Decide runs the rules in order (architecture.md): path boundary, owner-only commands,
// destructive Git, container management, process killing, direct server starts, secret access,
// publication. The first that does not allow decides.
func Decide(s Scope, c Call) (d Decision) {
	defer func() {
		if r := recover(); r != nil {
			d = deny("error", "aw could not check this call (%v), so it is refused → report this to the workspace maintainer", r)
		}
		if d.Verdict == Ask && !s.AskSupported {
			d.Verdict = Deny
		}
	}()
	if s.Home == "" {
		s.Home = homeOf()
	}
	if !filepath.IsAbs(c.Cwd) {
		return deny("error", "the tool call has no absolute working directory, so it is refused")
	}
	s.Owner = shell.Resolve(s.Owner)
	cwd := shell.Dir{Logical: filepath.Clean(c.Cwd), Physical: shell.Resolve(c.Cwd), Known: true}

	switch c.Tool {
	case Read, Write:
		return decideFiles(s, c, cwd)
	case Shell:
		return decideShell(s, c.Command, cwd)
	}
	return allow()
}

func decideFiles(s Scope, c Call, cwd shell.Dir) Decision {
	if s.fromOutsideLane(cwd.Physical) {
		// A harness that keeps running from the owner checkout: a relative path would mean the
		// owner's file, so paths must be absolute, and a search must say where.
		if len(c.Paths) == 0 {
			return deny("boundary", "this session belongs to lane %s, but it runs from the owner checkout → pass an explicit path inside the lane, %s", s.LaneName, quote(s.LaneRoot))
		}
		for _, p := range c.Paths {
			if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
				return deny("boundary", "this session belongs to lane %s, but it runs from the owner checkout, so %s would mean the owner's file → use an absolute path inside the lane, %s", s.LaneName, quote(p), quote(s.LaneRoot))
			}
		}
	}
	for _, p := range c.Paths {
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") || p == "~" {
			p = s.Home + p[1:]
		}
		abs, _ := absolute(p, cwd)
		if !s.isSecret(abs) {
			if d := s.boundary(abs, p); d.Verdict != Allow {
				return d
			}
		} else {
			if c.Tool == Write {
				return deny("secrets", "%s is a secret file, linked read-only into the lane; it is not changed from a lane → ask the human to change it in the owner checkout", quote(p))
			}
			return deny("secrets", "%s is a secret file, and agents do not read secrets → tools that need it read it themselves (aw run sets up the environment)", quote(p))
		}
		if c.Tool == Write {
			if d := s.unattachedWrite(abs); d.Verdict != Allow {
				return d
			}
			if d := s.selfModification(abs, p); d.Verdict != Allow {
				return d
			}
			if d := s.guardsOnPath(abs); d.Verdict != Allow {
				return d
			}
		}
	}
	return allow()
}

func decideShell(s Scope, src string, cwd shell.Dir) Decision {
	p, err := shell.Parse(src, cwd, &shell.Env{Home: s.Home})
	if err != nil {
		return deny("parse", "%v, so it is refused → write it as plain commands, without the construct", err)
	}
	var asks []Decision
	for _, c := range p.Commands {
		for _, rule := range []func(Scope, shell.Command) Decision{
			computedRule, pathsRule, ownerOnlyRule, gitRule, containerRule, killRule, serverRule, secretRule, muxRule, selfModRule, unattachedRule, publicationRule, teamGuardRule,
		} {
			switch d := rule(s, c); d.Verdict {
			case Deny:
				return d
			case Ask:
				asks = append(asks, d)
			}
		}
	}
	if len(asks) > 0 {
		return asks[0]
	}
	return allow()
}

// fromOutsideLane reports whether a lane session is working from elsewhere in the owner's
// workspace: a harness that keeps its first directory runs from the owner checkout after attaching.
func (s Scope) fromOutsideLane(p string) bool {
	return s.Role == Lane && !s.inLane(p) && within(p, s.Owner)
}

func (s Scope) inLane(p string) bool {
	return slices.ContainsFunc(s.LaneDirs, func(d string) bool { return within(p, d) })
}

// boundary refuses a lane session a path in the owner's workspace outside its lane: the owner
// checkout, another lane, another lane's state. Paths elsewhere, such as /tmp, are not the
// boundary's concern.
func (s Scope) boundary(abs, given string) Decision {
	if s.Role != Lane || abs == "" {
		return allow()
	}
	if within(abs, s.Owner) && !s.inLane(abs) {
		return deny("boundary", "%s is outside lane %s (it resolves to %s, in the owner's workspace) → work inside the lane, under %s", quote(given), s.LaneName, quote(abs), quote(s.LaneRoot))
	}
	return allow()
}

func (s Scope) isSecret(abs string) bool {
	return slices.Contains(s.Secrets, abs) || slices.ContainsFunc(s.Secrets, func(sec string) bool { return shell.Resolve(sec) == abs })
}

// attachHint names both ways forward. The grant is spelled out with this session's ID, because the
// human chooses the session by naming it, and only the agent's harness knows the ID.
func (s Scope) attachHint() string {
	pair := "aw pair"
	if s.Session != "" {
		pair = "aw pair --session " + quote(s.Session)
	}
	return `→ run bin/aw attach "<what you are working on>" to claim a lane, and work there; or ask the human to make this a pair session by running ` + pair
}

// unattachedWrite is the owner checkout's rule, so a file elsewhere on the machine is not its concern.
func (s Scope) unattachedWrite(abs string) Decision {
	if s.Role != Unattached || !within(abs, s.Owner) {
		return allow()
	}
	return deny("attach", "this session is not attached to a lane, and the owner checkout belongs to the human %s", s.attachHint())
}

// selfModification asks before a pair session changes the workspace's own instructions, hooks,
// guards or configuration (R63), and refuses a change inside a Git directory, where its own record
// is: the human alone grants pairing (R60). In a lane such a change is on the lane's branch, and
// merging it is the review.
func (s Scope) selfModification(abs, given string) Decision {
	if s.Role != Pair || !within(abs, s.Owner) {
		return allow()
	}
	rel, err := filepath.Rel(s.Owner, abs)
	if err != nil {
		return allow()
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if slices.Contains(parts, ".git") || within(abs, filepath.Join(s.Owner, workspace.LocalStateDir)) {
		return deny("self-modification", "%s is inside a Git directory or aw's state directory, which holds the workspace's hooks, shared configuration and aw's own records, including this session's → ask the human to change it", quote(given))
	}
	top := parts[0]
	switch {
	case rel == "workspace.toml", rel == "AGENTS.md", rel == "CLAUDE.md", top == ".aw", top == "bin",
		top == ".claude", top == ".codex", top == ".grok", top == ".opencode", top == ".githooks":
		return ask("self-modification", "%s is part of the workspace's own instructions, hooks or configuration, which the human reviews before it changes (R63)", quote(given))
	}
	return allow()
}

// Quote shows a path the way a shell would need it.
func Quote(s string) string { return quote(s) }

// quote shows a path the way a shell would need it, so a message never suggests a command that
// splits or expands it.
func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
