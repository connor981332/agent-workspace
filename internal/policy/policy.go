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
	if d := s.checkCwd(cwd); d.Verdict != Allow {
		return d
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
			if d := s.unattachedWrite(); d.Verdict != Allow {
				return d
			}
			if d := s.selfModification(abs, p); d.Verdict != Allow {
				return d
			}
		}
	}
	return allow()
}

func decideShell(s Scope, src string, cwd shell.Dir) Decision {
	if d := s.checkCwd(cwd); d.Verdict != Allow {
		return d
	}
	p, err := shell.Parse(src, cwd, &shell.Env{Home: s.Home})
	if err != nil {
		return deny("parse", "%v, so it is refused → write it as plain commands, without the construct", err)
	}
	var asks []Decision
	for _, c := range p.Commands {
		for _, rule := range []func(Scope, shell.Command) Decision{
			pathsRule, ownerOnlyRule, gitRule, containerRule, killRule, serverRule, secretRule, unattachedRule, publicationRule,
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

// checkCwd refuses a lane session that works outside its lane.
func (s Scope) checkCwd(cwd shell.Dir) Decision {
	if s.Role != Lane {
		return allow()
	}
	if !s.inLane(cwd.Physical) && within(cwd.Physical, s.Owner) {
		return deny("boundary", "this session belongs to lane %s, but its working directory %s is outside it → cd %s", s.LaneName, quote(cwd.Logical), quote(s.LaneRoot))
	}
	return allow()
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

const attachHint = `→ run bin/aw attach "<what you are working on>" to claim a lane, and work there; or ask the human to make this a pair session with aw pair`

func (s Scope) unattachedWrite() Decision {
	if s.Role != Unattached {
		return allow()
	}
	return deny("attach", "this session is not attached to a lane, and the owner checkout belongs to the human %s", attachHint)
}

// selfModification asks before a pair session changes the workspace's own instructions, hooks,
// guards or configuration (R63). In a lane such a change is on the lane's branch, and merging it
// is the review.
func (s Scope) selfModification(abs, given string) Decision {
	if s.Role != Pair || !within(abs, s.Owner) {
		return allow()
	}
	rel, err := filepath.Rel(s.Owner, abs)
	if err != nil {
		return allow()
	}
	top := strings.Split(rel, string(filepath.Separator))[0]
	switch {
	case rel == "workspace.toml", rel == "AGENTS.md", rel == "CLAUDE.md", top == ".aw", top == "bin",
		top == ".claude", top == ".codex", top == ".grok", top == ".opencode", top == ".githooks":
		return ask("self-modification", "%s is part of the workspace's own instructions, hooks or configuration, which the human reviews before it changes (R63)", quote(given))
	}
	return allow()
}

// quote shows a path the way a shell would need it, so a message never suggests a command that
// splits or expands it.
func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
