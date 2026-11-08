// Package ui holds terminal concerns: whether to use color, and making untrusted text (names,
// paths, values from configuration) safe to print.
package ui

import (
	"os"
	"strings"
	"unicode"

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
