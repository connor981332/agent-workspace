// Package platform holds what differs between Linux and macOS: process identity (PID plus start
// time, so a reused PID is never mistaken for the process it replaced) and the boot.
package platform

import "errors"

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
