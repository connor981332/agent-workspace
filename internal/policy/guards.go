package policy

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
)

// TeamGuard is a guard the team declared (R54, S9). It runs in-process, so it costs almost nothing
// on the hot path.
type TeamGuard struct {
	Name string
	// Commands are command patterns, as words. * matches any one word, and a word may hold wildcards.
	Commands [][]string
	// Paths are patterns of files agents may not write, from the workspace root. ** matches any
	// number of directories.
	Paths   []string
	Ask     bool
	Message string
	// Requires are files that must exist, from the workspace root. While one is missing the guard
	// cannot do its job, so everything it would match is refused.
	Requires []string
}

func (s Scope) guardDecision(g TeamGuard) Decision {
	for _, r := range g.Requires {
		if _, err := os.Stat(filepath.Join(s.Owner, r)); err != nil {
			return deny("team-guard", "team guard %s needs %s, which is missing, so this call is refused → restore the file, or ask the human", g.Name, quote(r))
		}
	}
	if g.Ask {
		return ask("team-guard", "%s (team guard %s)", g.Message, g.Name)
	}
	return deny("team-guard", "%s (team guard %s)", g.Message, g.Name)
}

// wordMatches matches one pattern word. A word whose value is unknown could be anything, so it
// matches: a guard must not be passed by computing the word (R55).
func wordMatches(pattern string, w shell.Word) bool {
	if !w.Known {
		return true
	}
	if pattern == w.Text {
		return true
	}
	ok, err := path.Match(pattern, w.Text)
	return ok && err == nil
}

// commandMatches reports whether a command fits a pattern: the same program, then each remaining
// pattern word in order among the command's arguments. Options are skipped unless the pattern names
// one, and an option's value may sit between the words (kubectl --context x delete).
func commandMatches(pattern []string, c shell.Command) bool {
	if len(pattern) == 0 || len(c.Args) == 0 {
		return false
	}
	if !c.Args[0].Known {
		return true
	}
	if !wordMatches(pattern[0], c.Args[0]) && !wordMatches(pattern[0], shell.Word{Text: c.Name(), Known: true}) {
		return false
	}
	want := pattern[1:]
	for _, a := range c.Args[1:] {
		if len(want) == 0 {
			break
		}
		if a.Known && strings.HasPrefix(a.Text, "-") && !strings.HasPrefix(want[0], "-") {
			continue
		}
		if wordMatches(want[0], a) {
			want = want[1:]
		}
	}
	return len(want) == 0
}

// workspaceRelative is a physical path as the workspace's own layout names it: a lane's copy of a
// file (.worktrees/01/x, web/.worktrees/01/x) is that file (x, web/x).
func (s Scope) workspaceRelative(abs string) (string, bool) {
	if !within(abs, s.Owner) {
		return "", false
	}
	rel, err := filepath.Rel(s.Owner, abs)
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, p := range parts {
		if p == ".worktrees" && i+1 < len(parts) {
			parts = append(parts[:i:i], parts[i+2:]...)
			break
		}
	}
	return strings.Join(parts, "/"), true
}

// pathMatches matches a slash-separated path against a pattern, where ** spans directories.
func pathMatches(pattern, p string) bool {
	return segmentsMatch(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func segmentsMatch(pattern, p []string) bool {
	if len(pattern) == 0 {
		return len(p) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(p); i++ {
			if segmentsMatch(pattern[1:], p[i:]) {
				return true
			}
		}
		return false
	}
	if len(p) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], p[0])
	return ok && err == nil && segmentsMatch(pattern[1:], p[1:])
}

func (s Scope) guardsOnPath(abs string) Decision {
	rel, ok := s.workspaceRelative(abs)
	if !ok {
		return allow()
	}
	for _, g := range s.Guards {
		for _, p := range g.Paths {
			if pathMatches(p, rel) {
				return s.guardDecision(g)
			}
		}
	}
	return allow()
}

// teamGuardRule applies the team's guards to a shell command. A write whose target cannot be
// determined might be to a guarded file, so it is treated as one.
func teamGuardRule(s Scope, c shell.Command) Decision {
	if len(s.Guards) == 0 {
		return allow()
	}
	for _, g := range s.Guards {
		for _, p := range g.Commands {
			if commandMatches(p, c) {
				return s.guardDecision(g)
			}
		}
	}
	targets := writeTargets(c)
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) && !harmless[r.Target.Text] {
			targets = append(targets, r.Target)
		}
	}
	first := allow()
	for _, t := range targets {
		d := allow()
		abs, ok := "", false
		if t.Known {
			abs, ok = absolute(t.Text, c.Dir)
		}
		if ok {
			d = s.guardsOnPath(abs)
		} else {
			d = s.unknownGuardedWrite(t)
		}
		switch d.Verdict {
		case Deny:
			return d
		case Ask:
			if first.Verdict == Allow {
				first = d
			}
		}
	}
	return first
}

// unknownGuardedWrite decides a write to a path aw cannot determine, while the team guards some
// paths: it might be one of them. The message says that, and names no guard, since none is known to
// match. If every such guard only asks, so does this.
func (s Scope) unknownGuardedWrite(t shell.Word) Decision {
	guarded, asks := false, true
	for _, g := range s.Guards {
		if len(g.Paths) > 0 {
			guarded, asks = true, asks && g.Ask
		}
	}
	if !guarded {
		return allow()
	}
	what := "a path"
	if t.Text != "" {
		what = quote(t.Text) + ", a path"
	}
	if asks {
		return ask("team-guard", "this command writes to %s aw cannot determine, and the team guards some files in this workspace → approve it if it is none of them, or write the path literally", what)
	}
	return deny("team-guard", "this command writes to %s aw cannot determine, and the team guards some files in this workspace, so it is refused → write the path literally; a variable set earlier in the same command is fine", what)
}
