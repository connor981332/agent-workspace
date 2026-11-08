//go:build darwin

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ProcessIdentity is the process's start time ("<seconds>.<microseconds>"), from sysctl.
//
// For a PID with no process, sysctl succeeds with an empty result, which x/sys reports as EIO, so
// any sysctl failure is checked with kill(pid, 0): ESRCH there means the process is gone. A zombie
// has finished, so it is gone too.
func ProcessIdentity(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return "", ErrGone
		}
		return "", ErrUnknown
	}
	if kp.Proc.P_pid != int32(pid) || kp.Proc.P_stat == sZombie {
		return "", ErrGone
	}
	t := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), nil
}

// sZombie is SZOMB from <sys/proc.h>.
const sZombie = 5
