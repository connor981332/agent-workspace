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
	"path/filepath"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/workspace"
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
	msg := tail(strings.TrimSpace(e.Stderr))
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s (in %s): %s", strings.Join(e.Args, " "), e.Dir, msg)
}

const (
	tailLines = 20
	lineWidth = 300
)

// tail keeps an error readable: the last lines of what Git and its hooks said, none of them long.
func tail(text string) string {
	lines := strings.Split(text, "\n")
	cut := 0
	if len(lines) > tailLines {
		cut = len(lines) - tailLines
		lines = lines[cut:]
	}
	for i, l := range lines {
		if len(l) > lineWidth {
			lines[i] = l[:lineWidth] + "…"
		}
	}
	out := strings.Join(lines, "\n")
	if cut > 0 {
		out = fmt.Sprintf("(%d earlier lines not shown)\n%s", cut, out)
	}
	return out
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
	// Config are -c settings for every command.
	Config []string
}

// In returns a Git for dir, with any -c settings given as key=value.
func In(dir string, config ...string) Git { return Git{Dir: dir, Config: config} }

// HookPath is the executable hook a repository's team would run, or "". It looks where aw setup
// recorded the team's hooks directory, then where Git itself would look. A relative result is
// relative to the top of a worktree, as core.hooksPath is.
func HookPath(ctx context.Context, dir, name string) string {
	// One Git call, because lane claims come through here.
	listed, _ := In(dir).Run(ctx, "config", "--local", "--get-regexp", `^(aw\.teamhookspath|core\.hookspath)$`)
	hooks, core := "", ""
	for _, line := range strings.Split(listed, "\n") {
		if v, ok := strings.CutPrefix(line, "aw.teamhookspath "); ok {
			hooks = v
		} else if v, ok := strings.CutPrefix(line, "core.hookspath "); ok {
			core = v
		}
	}
	if hooks == "" {
		hooks = core
	}
	if hooks == "" {
		common, err := workspace.GitCommonDir(dir)
		if err != nil {
			return ""
		}
		hooks = filepath.Join(common, "hooks")
	}
	path := filepath.Join(hooks, name)
	check := path
	if !filepath.IsAbs(check) {
		check = filepath.Join(dir, check)
	}
	if info, err := os.Stat(check); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return ""
	}
	return path
}

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
	full := args
	if len(g.Config) > 0 {
		full = nil
		for _, c := range g.Config {
			full = append(full, "-c", c)
		}
		full = append(full, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
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
