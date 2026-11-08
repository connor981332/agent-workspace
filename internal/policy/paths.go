package policy

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
)

// within reports whether p is dir or inside it. Both must be clean and absolute.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) || dir == "/"
}

// looksLikePath is C1's test for a literal that names a file: ".", "..", a leading /, ./, ../ or ~,
// or any slash. URLs are not paths.
func looksLikePath(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	return s == "." || s == ".." || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.Contains(s, "/")
}

// globPrefix cuts a path at its first glob character, so ../* is checked as ..
func globPrefix(s string) string {
	if i := strings.IndexAny(s, "*?["); i >= 0 {
		s = s[:i]
		if j := strings.LastIndex(s, "/"); j >= 0 {
			return s[:j+1]
		}
		return "."
	}
	return s
}

// special files any command may name.
var harmless = map[string]bool{
	"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/stdin": true, "/dev/tty": true,
	"/dev/zero": true, "/dev/random": true, "/dev/urandom": true, "/dev/fd/0": true, "/dev/fd/1": true,
	"/dev/fd/2": true,
}

// absolute resolves a path argument physically against the directory it is used in.
func absolute(p string, d shell.Dir) (string, bool) {
	p = globPrefix(p)
	if filepath.IsAbs(p) {
		return shell.Resolve(p), true
	}
	if !d.Known {
		return "", false
	}
	return shell.Resolve(filepath.Join(d.Physical, p)), true
}

// homeOf is the user's home directory, for ~ and $HOME.
func homeOf() string {
	h, _ := os.UserHomeDir()
	return h
}
