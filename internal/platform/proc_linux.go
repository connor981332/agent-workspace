//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcessIdentity is "<boot id>:<start time in clock ticks since boot>", from /proc. A reboot
// changes the boot id, so no process survives it.
func ProcessIdentity(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrGone
	} else if err != nil {
		return "", ErrUnknown
	}
	// The command name (field 2) is in parentheses and may contain spaces; fields after it are
	// space-separated. Start time is field 22, the 20th after the closing parenthesis.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", ErrUnknown
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return "", ErrUnknown
	}
	if fields[0] == "Z" {
		return "", ErrGone // finished, only waiting for its parent to collect it
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", ErrUnknown
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}

// Parent is pid's parent process.
func Parent(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, ErrGone
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, ErrUnknown
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0, ErrUnknown
	}
	return strconv.Atoi(fields[1])
}

// Names are the names a process goes by: its command name and the base of its first two arguments,
// because a harness may be a script run by an interpreter (node, sh).
func Names(pid int) []string {
	var out []string
	if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		out = append(out, strings.TrimSpace(string(comm)))
	}
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		for _, a := range args[:min(2, len(args))] {
			out = append(out, filepath.Base(a))
		}
	}
	return out
}
