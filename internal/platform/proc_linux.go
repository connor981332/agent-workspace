//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
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
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", ErrUnknown
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
