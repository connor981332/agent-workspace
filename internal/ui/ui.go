// Package ui holds terminal concerns: whether to use color, and making untrusted text (names,
// paths, values from configuration) safe to print.
package ui

import (
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// Color reports whether output to f may use color: f is a terminal and NO_COLOR is not set
// (https://no-color.org).
func Color(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(f)
}

// IsTerminal reports whether f is a terminal. (A character device is not enough: /dev/null is one.)
func IsTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// Clean removes control characters (C0, DEL and C1), so that text from configuration or the file
// system cannot move the cursor, change colors or hide output. Tabs become spaces.
func Clean(s string) string {
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// Width is the width output should fit: $COLUMNS, else the terminal's, else 0 (no limit).
func Width(f *os.File) int {
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
		return c
	}
	if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
		return w
	}
	return 0
}

// Fit cuts a line to width runes, ending it with "…". A width of 0 or less means no limit.
func Fit(s string, width int) string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// Brief shortens text to at most max runes, cutting at the last clause boundary (". ", "; ", ", ")
// that keeps at least half of it, else at the last space, and ending with "…".
func Brief(s string, max int) string {
	s = strings.Join(strings.Fields(Clean(s)), " ")
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	cut := string(r[:max-1])
	for _, sep := range []string{". ", "; ", ", "} {
		if i := strings.LastIndex(cut, sep); i >= utf8.RuneCountInString(cut)/2 {
			return strings.TrimRight(cut[:i+len(sep)-1], ",;") + " …"
		}
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		return cut[:i] + "…"
	}
	return cut + "…"
}
