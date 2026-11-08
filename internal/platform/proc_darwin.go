//go:build darwin

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ProcessIdentity is the process's start time ("<seconds>.<microseconds>"), from sysctl.
func ProcessIdentity(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) {
		return "", ErrGone
	} else if err != nil {
		return "", ErrUnknown
	}
	if kp.Proc.P_pid != int32(pid) {
		return "", ErrGone
	}
	t := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), nil
}
