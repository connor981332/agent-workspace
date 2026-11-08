// Package mux opens a session in a terminal multiplexer (architecture.md, "Terminal multiplexers"):
// herdr and tmux are built in, and any other is a command template. A session's liveness never
// depends on this: it follows the harness process, which the session's first hook call records.
package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
)

// Launch is what to run, and where.
type Launch struct {
	Name string // shown as the window's or tab's name
	Dir  string
	Env  []string // KEY=VALUE, added to the command's environment
	Argv []string
}

// Quote quotes a word for a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// shell is the launch as one shell command, which carries its own directory and environment, since
// a multiplexer's server has neither.
func (l Launch) shell() string {
	var b strings.Builder
	b.WriteString("cd " + Quote(l.Dir) + " && exec env")
	for _, e := range l.Env {
		b.WriteString(" " + Quote(e))
	}
	for _, a := range l.Argv {
		b.WriteString(" " + Quote(a))
	}
	return b.String()
}

// ErrNone means no multiplexer is configured.
var ErrNone = errors.New(`--mux needs a multiplexer → set multiplexer in your individual layer: "herdr", "tmux", or { command = "… {name} -- {cmd}" }`)

// Check reports, before anything is claimed or recorded, why --mux could not open anything.
func Check(m config.Multiplexer) error {
	if m.Command != "" {
		return nil
	}
	if m.Name != "tmux" && m.Name != "herdr" {
		return ErrNone
	}
	if _, err := exec.LookPath(m.Name); err != nil {
		return fmt.Errorf("the multiplexer %s is not installed (not on PATH) → install it, or change multiplexer in your individual layer", m.Name)
	}
	return nil
}

// Open starts the launch and says where it went. background keeps the human's focus where it is,
// for opening several at once.
func Open(ctx context.Context, m config.Multiplexer, l Launch, background bool) (string, error) {
	switch {
	case m.Command != "":
		return openTemplate(ctx, m.Command, l)
	case m.Name == "tmux":
		return openTmux(ctx, l, background)
	case m.Name == "herdr":
		return openHerdr(ctx, l, background)
	}
	return "", ErrNone
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("the multiplexer %s is not installed (not on PATH) → install it, or change multiplexer in your individual layer", name)
	}
	var stderr strings.Builder
	c := exec.CommandContext(ctx, name, args...)
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s failed: %v: %s", name, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func openTmux(ctx context.Context, l Launch, background bool) (string, error) {
	if os.Getenv("TMUX") != "" {
		args := []string{"new-window", "-n", l.Name}
		if background {
			args = []string{"new-window", "-d", "-n", l.Name}
		}
		if _, err := run(ctx, "tmux", append(args, l.shell())...); err != nil {
			return "", err
		}
		return "a new tmux window, " + l.Name, nil
	}
	if _, err := run(ctx, "tmux", "new-session", "-d", "-s", l.Name, l.shell()); err != nil {
		return "", err
	}
	return "tmux session " + l.Name + " (tmux attach -t " + Quote(l.Name) + ")", nil
}

func openHerdr(ctx context.Context, l Launch, background bool) (string, error) {
	focus := "--focus"
	if background {
		focus = "--no-focus"
	}
	out, err := run(ctx, "herdr", "tab", "create", "--cwd", l.Dir, "--label", l.Name, focus)
	if err != nil {
		return "", err
	}
	pane, err := rootPane(out)
	if err != nil {
		return "", err
	}
	if _, err := run(ctx, "herdr", "pane", "run", pane, l.shell()); err != nil {
		return "", err
	}
	return "a new herdr tab, " + l.Name, nil
}

// rootPane reads the new tab's pane from herdr's reply.
func rootPane(reply []byte) (string, error) {
	var r struct {
		Result struct {
			RootPane struct {
				PaneID json.RawMessage `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reply, &r); err != nil || len(r.Result.RootPane.PaneID) == 0 {
		return "", fmt.Errorf("herdr's reply did not name the new tab's pane (%q) → check that this version of herdr is supported", strings.TrimSpace(string(reply)))
	}
	id := string(r.Result.RootPane.PaneID)
	if s, err := strconv.Unquote(id); err == nil {
		id = s
	}
	return id, nil
}

func openTemplate(ctx context.Context, template string, l Launch) (string, error) {
	line := strings.NewReplacer("{name}", Quote(l.Name), "{cmd}", "sh -c "+Quote(l.shell())).Replace(template)
	var stderr strings.Builder
	c := exec.CommandContext(ctx, "sh", "-c", line)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("the multiplexer command failed: %v: %s → check multiplexer in your individual layer", err, strings.TrimSpace(stderr.String()))
	}
	return "your multiplexer", nil
}
