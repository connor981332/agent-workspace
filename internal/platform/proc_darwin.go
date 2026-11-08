//go:build darwin

package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"

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

// Parent is pid's parent process.
func Parent(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, ErrGone
	}
	return int(kp.Eproc.Ppid), nil
}

// Names are the names a process goes by: its command name and the base of its first two arguments,
// because a harness may be a script run by an interpreter (node, sh).
func Names(pid int) []string {
	var out []string
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return nil
	}
	comm := make([]byte, 0, len(kp.Proc.P_comm))
	for _, c := range kp.Proc.P_comm {
		if c == 0 {
			break
		}
		comm = append(comm, byte(c))
	}
	out = append(out, string(comm))
	// kern.procargs2: argc, the executable path, padding NULs, then argv. Other users' are refused.
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return out
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return out
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	for n := 0; n < min(2, argc) && len(rest) > 0; n++ {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			end = len(rest)
		}
		out = append(out, filepath.Base(string(rest[:end])))
		rest = rest[min(end+1, len(rest)):]
	}
	return out
}
