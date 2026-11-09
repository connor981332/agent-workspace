// Package procs starts, tracks and stops process groups (resources.md). Each server runs in its own
// session and process group, with its output in a log. A process is identified by its PID and start
// time, and one whose identity is uncertain is never signalled.
package procs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/connor981332/agent-workspace/internal/platform"
)

// Proc is a started process group.
type Proc struct {
	PID   int
	Start string
	Log   string
}

// Spec is what to start.
type Spec struct {
	Command string // a shell command
	Dir     string
	Env     []string // added to this process's environment
	Log     string
	Ready   string // a shell command that exits 0 when the server is ready, or ""
	// Argv and ReadyArgv replace Command and Ready for a server in a container: a keeper, and a probe
	// through docker exec.
	Argv      []string
	ReadyArgv []string
}

// settle is how long a server with no readiness probe must stay up to count as started.
const settle = 100 * time.Millisecond

// readyLimit is how long a readiness probe may take to pass.
const readyLimit = 2 * time.Minute

// Start starts a command in a new session, so that it outlives aw and can be signalled as a group.
func Start(ctx context.Context, s Spec) (Proc, error) {
	if err := os.MkdirAll(filepath.Dir(s.Log), 0o700); err != nil {
		return Proc{}, err
	}
	log, err := os.OpenFile(s.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Proc{}, err
	}
	defer log.Close()
	c := exec.Command("sh", "-c", s.Command)
	if s.Argv != nil {
		c = exec.Command(s.Argv[0], s.Argv[1:]...)
	}
	c.Dir, c.Env = s.Dir, append(os.Environ(), s.Env...)
	c.Stdout, c.Stderr = log, log
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return Proc{}, fmt.Errorf("starting %q: %w", s.Command, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	p := Proc{PID: c.Process.Pid, Log: s.Log}
	if p.Start, err = platform.ProcessIdentity(p.PID); err != nil {
		p.Start = ""
	}
	fail := func(why string) (Proc, error) {
		return p, fmt.Errorf("%s → its log is %s", why, s.Log)
	}
	if s.Ready == "" && s.ReadyArgv == nil {
		select {
		case err := <-exited:
			return fail(fmt.Sprintf("the server exited at once (%v)", err))
		case <-time.After(settle):
			return p, nil
		case <-ctx.Done():
			Stop(p, 0)
			return p, ctx.Err()
		}
	}
	deadline := time.Now().Add(readyLimit)
	for {
		probe := exec.CommandContext(ctx, "sh", "-c", s.Ready)
		if s.ReadyArgv != nil {
			probe = exec.CommandContext(ctx, s.ReadyArgv[0], s.ReadyArgv[1:]...)
		}
		probe.Dir, probe.Env = s.Dir, c.Env
		if probe.Run() == nil {
			return p, nil
		}
		select {
		case err := <-exited:
			return fail(fmt.Sprintf("the server exited before it was ready (%v)", err))
		case <-ctx.Done():
			Stop(p, 0)
			return p, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			Stop(p, 0)
			return fail("the server was not ready within 2 minutes")
		}
	}
}

func groupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// signalable reports whether this group is still the one that was started. A leader whose identity
// cannot be read, or whose PID now belongs to another process, is left alone.
func signalable(p Proc) bool {
	if p.PID <= 1 || p.Start == "" {
		return false
	}
	got, err := platform.ProcessIdentity(p.PID)
	switch {
	case errors.Is(err, platform.ErrGone):
		return groupExists(p.PID) // the leader has gone; what is left of its group is still ours
	case err != nil:
		return false
	}
	return got == p.Start
}

// Alive reports whether the group's leader is still the process that was started. Unknown counts as
// alive: unknown means protected.
func Alive(p Proc) bool { return platform.Alive(p.PID, p.Start) }

// Signal sends SIGTERM to the group, and reports whether it did.
func Signal(p Proc) bool {
	return signalable(p) && syscall.Kill(-p.PID, syscall.SIGTERM) == nil
}

// Stop ends the group: SIGTERM, then SIGKILL after grace. It never touches a group it cannot verify.
// A container command whose keeper has died is stopped from the keeper's record.
func Stop(p Proc, grace time.Duration) {
	if !Signal(p) {
		Reap(p.Log, grace)
		return
	}
	Finish(p, grace)
}

// Finish waits up to grace for a signalled group to go, then kills what is left.
func Finish(p Proc, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for groupExists(p.PID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if groupExists(p.PID) {
		_ = syscall.Kill(-p.PID, syscall.SIGKILL)
	}
	Reap(p.Log, 0)
}
