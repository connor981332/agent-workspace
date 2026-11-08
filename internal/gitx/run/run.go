// Package run runs Git as a subprocess, for operations that need Git's full logic: worktrees,
// branches, merges and ancestry. It is not on the hot path (plans/release-candidate.md §4.3).
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ExtraEnv is added to every Git command's environment. aw sets its ref token here, once, before it
// starts work (lanes.Manager.authorizeRefs).
var ExtraEnv []string

// DefaultTimeout bounds one Git command.
const DefaultTimeout = 2 * time.Minute

// Error is a failed Git command, with its standard error.
type Error struct {
	Dir    string
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s (in %s): %s", strings.Join(e.Args, " "), e.Dir, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns Git's exit status, or -1.
func (e *Error) ExitCode() int {
	var ee *exec.ExitError
	if errors.As(e.Err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Git runs commands in one directory.
type Git struct {
	Dir string
	// Env is added to the process environment.
	Env []string
}

// In returns a Git for dir.
func In(dir string) Git { return Git{Dir: dir} }

// Run runs git with args and returns its standard output.
func (g Git) Run(ctx context.Context, args ...string) (string, error) {
	return g.RunInput(ctx, nil, args...)
}

// RunInput is Run with standard input.
func (g Git) RunInput(ctx context.Context, stdin []byte, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	// Never prompt, never page, and read plain output.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	cmd.Env = append(cmd.Env, ExtraEnv...)
	cmd.Env = append(cmd.Env, g.Env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), &Error{Dir: g.Dir, Args: args, Stderr: errOut.String(), Err: err}
	}
	return out.String(), nil
}

// Line runs git and returns its output with the trailing newline removed.
func (g Git) Line(ctx context.Context, args ...string) (string, error) {
	out, err := g.Run(ctx, args...)
	return strings.TrimRight(out, "\n"), err
}

// OK runs git and reports whether it exited 0. Other failures (a missing git) are errors.
func (g Git) OK(ctx context.Context, args ...string) (bool, error) {
	_, err := g.Run(ctx, args...)
	var ge *Error
	if errors.As(err, &ge) && ge.ExitCode() > 0 {
		return false, nil
	}
	return err == nil, err
}
