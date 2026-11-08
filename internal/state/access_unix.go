//go:build linux || darwin

package state

import (
	"os"
	"syscall"
)

// writable reports whether this process may create files in dir.
func writable(dir string) bool { return syscall.Access(dir, 2 /* W_OK */) == nil }

// lockFile takes an exclusive lock on path, creating it, and returns the function that releases it.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
