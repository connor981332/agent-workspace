// Package platform holds what differs between Linux and macOS: process identity (PID plus start
// time, so a reused PID is never mistaken for the process it replaced) and the boot.
package platform

import (
	"errors"
	"os"
	"strconv"
)

// ErrUnknown means a process's identity cannot be read (for example, permission). Callers treat
// such a process as alive: unknown means protected.
var ErrUnknown = errors.New("process identity unknown")

// ErrGone means no process has that PID.
var ErrGone = errors.New("no such process")

// Alive reports whether pid is still the process recorded with identity. A process whose identity
// cannot be read counts as alive.
func Alive(pid int, identity string) bool {
	if pid <= 0 || identity == "" {
		return true // nothing to compare: protect
	}
	got, err := ProcessIdentity(pid)
	switch {
	case errors.Is(err, ErrGone):
		return false
	case err != nil:
		return true
	}
	return got == identity
}

// HarnessAncestor returns the nearest ancestor of pid that goes by name, or 0. A hook or a tool
// command is a descendant of its harness, so its ancestry names the live harness process.
//
// AW_ANCESTRY_STOP=<pid> ends the search at that process, so a test run started from inside a harness
// does not find the harness above the test runner.
func HarnessAncestor(pid int, name string) int {
	stop, _ := strconv.Atoi(os.Getenv("AW_ANCESTRY_STOP"))
	for range 32 {
		parent, err := Parent(pid)
		if err != nil || parent <= 1 || parent == stop {
			return 0
		}
		for _, n := range Names(parent) {
			if n == name {
				return parent
			}
		}
		pid = parent
	}
	return 0
}
