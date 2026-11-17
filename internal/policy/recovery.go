package policy

import (
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
)

// decideBroken decides a call while the workspace's configuration cannot be read. The guards fail
// closed, except for the way back out: aw's diagnostics, Git commands that only read what Git
// tracks, and, for a pair session acting for the human, switching branch and upgrading aw.
// Anything else is refused with what still works.
func decideBroken(s Scope, c Call, cwd shell.Dir) Decision {
	refuse := func() Decision {
		also := ""
		if s.Role == Pair {
			also = ", git switch, bin/aw upgrade"
		}
		return deny("config", "the workspace configuration is invalid, so guards cannot decide: %s → bin/aw doctor, bin/aw config validate, git status, git log and git diff%s still work; the human fixes workspace.toml, or runs bin/aw upgrade if it needs a newer aw", s.Broken, also)
	}
	if c.Tool != Shell {
		return refuse() // the secrets a read could reach are named in the configuration
	}
	p, err := shell.Parse(c.Command, cwd, &shell.Env{Home: s.Home})
	if err != nil || len(p.Commands) == 0 {
		return refuse()
	}
	for _, cmd := range p.Commands {
		if !recoveryCommand(s, cmd) {
			return refuse()
		}
	}
	return allow()
}

// recoveryCommand is one command of the way back out.
func recoveryCommand(s Scope, c shell.Command) bool {
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) && !harmless[r.Target.Text] {
			return false
		}
	}
	for _, a := range c.Args {
		if !a.Known {
			return false
		}
	}
	if len(c.Args) == 0 {
		return true
	}
	args := c.Args[1:]
	switch c.Name() {
	case "cd", "true", "pwd":
		return true
	case "aw":
		sub, rest := subcommand(args)
		switch sub {
		case "doctor", "version", "whoami", "changelog":
			return true
		case "config":
			st, _ := subcommand(rest)
			return st == "validate" || st == "explain" || st == "get"
		case "upgrade":
			return s.Role == Pair
		}
	case "git":
		_, sub, rest, unknown := gitGlobal(args)
		if unknown {
			return false
		}
		switch sub {
		case "status", "log", "show", "rev-parse", "fetch":
			return true
		case "diff":
			return !hasArg(rest, "--no-index") // an untracked file may be a secret
		case "branch":
			return isReadOnly(c)
		case "switch", "checkout":
			// Git itself refuses to switch over uncommitted changes, unless forced.
			return s.Role == Pair && !hasArg(rest, "--force", "--discard-changes", "--orphan", "--") &&
				!hasShortFlag(rest, 'f') && !slices.ContainsFunc(rest, func(w shell.Word) bool { return w.Text == "." })
		}
	}
	return false
}
