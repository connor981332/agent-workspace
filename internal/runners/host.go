// Package runners runs project commands: on the host (M2), or in a project's devcontainer (M6).
package runners

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// RunHost runs argv in dir with the extra environment, passing standard input, output and error
// through, and returns the exit status the caller should exit with. Signals sent to aw (SIGINT,
// SIGTERM, SIGHUP) are forwarded to the command; a command killed by a signal gives 128 plus the
// signal's number, as a shell would.
func RunHost(dir string, env, argv []string) (int, error) {
	if len(argv) == 0 {
		return 2, errors.New("no command to run")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return 127, fmt.Errorf("%s: command not found → check the command, or the project's PATH", argv[0])
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Off a terminal the command gets its own process group, so that a signal reaches everything it
	// started. On a terminal it stays in the foreground group, so that it can read the terminal;
	// the terminal's own signals reach it anyway.
	ownGroup := !term.IsTerminal(int(os.Stdin.Fd()))
	if ownGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return 126, err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				sig := s.(syscall.Signal)
				if ownGroup {
					syscall.Kill(-cmd.Process.Pid, sig)
				} else {
					cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return ee.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
