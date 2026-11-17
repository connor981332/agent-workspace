package inspect

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
)

// FileGroup is one line of what lanes would lack: a single file, or files that repeat (the same
// name in several directories, or many files of one kind in one directory) shown as the glob that
// covers them. Kind says what the files look like when aw recognizes them.
type FileGroup struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
	Kind    string `json:"kind,omitempty"` // test output, log, build state, editor backup
}

// Kinds aw recognizes from names alone, the same in any product. Only test output, logs and build
// state are suggested for generated: a backup copy may still be somebody's.
var fileKinds = []struct{ glob, kind string }{
	{"rspec.xml", "test output"}, {".rspec_status", "test output"}, {".rspec_examples", "test output"},
	{"junit*.xml", "test output"}, {".nyc_output", "test output"}, {".pytest_cache", "test output"},
	{"*.log", "log"},
	{"restart.txt", "build state"}, {"install-state.gz", "build state"}, {"*.tsbuildinfo", "build state"},
	{".eslintcache", "build state"}, {"*.pyc", "build state"}, {"Gemfile.lock", "build state"},
	{"*.swp", "editor backup"}, {"*.bak", "editor backup"}, {"*.orig", "editor backup"}, {"*~", "editor backup"},
}

func kindOf(name string) string {
	_, kind := globOf(name)
	return kind
}

// globOf is the known glob a name matches, and its kind.
func globOf(name string) (string, string) {
	for _, k := range fileKinds {
		if ok, _ := path.Match(k.glob, name); ok {
			return k.glob, k.kind
		}
	}
	return "", ""
}

// repeatAt is how often a name, or a kind of file in one directory, must repeat to be shown as one.
const repeatAt = 3

// groupFiles collapses entries that repeat into globs, largest groups first.
func groupFiles(entries []string) []FileGroup {
	byName := map[string][]string{}
	for _, e := range entries {
		byName[path.Base(strings.TrimSuffix(e, "/"))] = append(byName[path.Base(strings.TrimSuffix(e, "/"))], e)
	}
	var out []FileGroup
	var rest []string
	for name, list := range byName {
		if len(list) >= repeatAt {
			out = append(out, FileGroup{Pattern: "**/" + name, Count: len(list), Kind: kindOf(name)})
		} else {
			rest = append(rest, list...)
		}
	}
	// Then many files with one extension in one directory, such as notes-01.md to notes-62.md.
	byDirExt := map[string][]string{}
	for _, e := range rest {
		if ext := path.Ext(e); ext != "" && !strings.HasSuffix(e, "/") {
			byDirExt[path.Dir(e)+"\x00"+ext] = append(byDirExt[path.Dir(e)+"\x00"+ext], e)
		}
	}
	for key, list := range byDirExt {
		if len(list) < repeatAt {
			continue
		}
		dir, ext, _ := strings.Cut(key, "\x00")
		glob := "*" + ext
		if dir != "." {
			glob = dir + "/" + glob
		}
		out = append(out, FileGroup{Pattern: glob, Count: len(list), Kind: kindOf("x" + ext)})
		rest = slices.DeleteFunc(rest, func(e string) bool { return slices.Contains(list, e) })
	}
	for _, e := range rest {
		out = append(out, FileGroup{Pattern: e, Count: 1, Kind: kindOf(path.Base(strings.TrimSuffix(e, "/")))})
	}
	slices.SortFunc(out, func(a, b FileGroup) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Pattern, b.Pattern))
	})
	return out
}

// suggestGenerated is what generated could say for the groups aw recognizes as test output, logs
// or build state. A single file becomes a glob of its name at any depth only when its kind is known.
func suggestGenerated(groups []FileGroup) []string {
	var out []string
	for _, g := range groups {
		if g.Kind == "" || g.Kind == "editor backup" {
			continue
		}
		p := g.Pattern
		if g.Count == 1 {
			// A known glob such as *.log covers the file and its siblings; a plain name, at any depth.
			glob, _ := globOf(path.Base(strings.TrimSuffix(p, "/")))
			p = glob
			if !strings.ContainsAny(glob, "*?[") {
				p = "**/" + glob
			}
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// describe is a group as a line shows it.
func (g FileGroup) describe() string {
	switch {
	case g.Count > 1 && g.Kind != "":
		return fmt.Sprintf("%s (%d, %s)", g.Pattern, g.Count, g.Kind)
	case g.Count > 1:
		return fmt.Sprintf("%s (%d)", g.Pattern, g.Count)
	case g.Kind != "":
		return fmt.Sprintf("%s (%s)", g.Pattern, g.Kind)
	}
	return g.Pattern
}
