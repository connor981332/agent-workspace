// Package lanes creates, claims and lays out lanes: numbered, stable directories with a worktree of
// every lane-enabled project, named for the work in them (architecture.md, "On-disk layout";
// lifecycle.md).
package lanes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Leading words that say what kind of change it is, not what it is about.
var verbs = map[string]bool{
	"fix": true, "fixes": true, "fixing": true, "add": true, "adds": true, "adding": true, "update": true, "updates": true,
	"implement": true, "implements": true, "create": true, "make": true, "remove": true, "delete": true, "refactor": true,
	"improve": true, "support": true, "change": true, "rename": true, "move": true, "build": true, "write": true,
	"try": true, "investigate": true, "debug": true, "clean": true, "cleanup": true, "use": true, "allow": true,
	"handle": true, "enable": true, "disable": true, "upgrade": true, "bump": true, "port": true, "migrate": true,
}

// Words that carry no meaning in a name.
var filler = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "for": true, "of": true, "in": true, "on": true, "with": true,
	"and": true, "or": true, "into": true, "from": true, "at": true, "by": true, "is": true, "it": true, "its": true,
	"our": true, "my": true, "new": true, "some": true, "this": true, "that": true, "up": true,
}

// MaxNameLength bounds a lane name.
const MaxNameLength = 32

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidName reports whether s can name a lane.
func ValidName(s string) bool { return len(s) <= MaxNameLength && nameRE.MatchString(s) }

// CheckName returns an error that names the fix when s cannot name a lane. Names come from users and
// agents, so they are checked before they reach a path, a branch or a query.
func CheckName(s string) error {
	if !ValidName(s) {
		return fmt.Errorf("%q is not a lane name → use the name aw shows (lowercase letters, digits and hyphens, such as checkout-tax), or the lane number", s)
	}
	return nil
}

// Name derives a lane name from a purpose: "fix checkout tax rounding" → "checkout-tax". It keeps
// the first two meaningful words, skipping a leading verb and filler words, and falls back to
// "lane" when nothing is left.
func Name(purpose string) string {
	words := strings.FieldsFunc(strings.ToLower(purpose), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	var kept []string
	for i, w := range words {
		if (i == 0 && verbs[w]) || filler[w] || (i == 0 && w == "wip") {
			continue
		}
		kept = append(kept, w)
		if len(kept) == 2 {
			break
		}
	}
	if len(kept) == 0 {
		for _, w := range words {
			if !filler[w] {
				kept = []string{w}
				break
			}
		}
	}
	name := strings.Join(kept, "-")
	if name != "" && !unicode.IsLetter(rune(name[0])) {
		name = "lane-" + name
	}
	if len(name) > MaxNameLength {
		name = strings.TrimRight(name[:MaxNameLength], "-")
	}
	if !ValidName(name) {
		return "lane"
	}
	return name
}

// Unique returns name, or name-2, name-3 … when taken says it is in use.
func Unique(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		base := name
		if len(base)+len(suffix) > MaxNameLength {
			base = strings.TrimRight(base[:MaxNameLength-len(suffix)], "-")
		}
		if c := base + suffix; !taken(c) {
			return c
		}
	}
}

// Dir is a lane's directory name: 03, or 103 when lanes.max is over 99 (plan §9).
func Dir(n, max int) string {
	if max > 99 {
		return fmt.Sprintf("%03d", n)
	}
	return fmt.Sprintf("%02d", n)
}
