// Package mux opens a session in a terminal multiplexer (architecture.md, "Terminal multiplexers"):
// herdr and tmux are built in, and any other is a command template. A session's liveness never
// depends on this: it follows the harness process, which the session's first hook call records.
package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
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
	if m.Name == "herdr" {
		return checkHerdr()
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
		if herdrTool() != "herdr" {
			return openHerdrCore(ctx, l)
		}
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

// herdrCLIs are the command-line tools a Herdr install may have, in the order aw prefers them: the
// herdr CLI, with tab create and pane run, and Herdr Core's hc (also installed as herdr-core), with
// run (#134).
var herdrCLIs = []string{"herdr", "hc", "herdr-core"}

// herdrTool is the first of herdrCLIs on PATH, or "".
func herdrTool() string {
	for _, name := range herdrCLIs {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// checkHerdr reports why aw could not open a Herdr tab: no Herdr tool on PATH, or, with Herdr
// Core's hc, a terminal that is not one of Herdr's, since a new tab goes in the same space as this
// terminal.
func checkHerdr() error {
	switch herdrTool() {
	case "":
		return errors.New(`no Herdr command-line tool is on PATH (aw looks for herdr, hc and herdr-core) → install Herdr, or set multiplexer to { command = "… {name} -- {cmd}" } in your individual layer`)
	case "herdr":
		return nil
	}
	if os.Getenv("HC_TERMINAL_ID") == "" {
		return errors.New("this terminal is not one of Herdr's (HC_TERMINAL_ID is not set), and Herdr Core opens a tab only from inside one → run aw from a Herdr tab, or use --here")
	}
	return nil
}

// openHerdrCore opens a new tab in this terminal's Herdr space with Herdr Core's hc: hc run in that
// space, with no herdr.tab label, opens a new tab there. --no-parent keeps the tab when this
// terminal's program ends, and PATH is passed on, since Herdr's server does not have the user's.
func openHerdrCore(ctx context.Context, l Launch) (string, error) {
	tool := herdrTool()
	id := os.Getenv("HC_TERMINAL_ID")
	if id == "" {
		return "", checkHerdr()
	}
	out, err := run(ctx, tool, "get", id, "--json")
	if err != nil {
		return "", err
	}
	space := findLabel(out, "herdr.space")
	if space == "" {
		return "", fmt.Errorf("%s get %s did not name this terminal's Herdr space (%q) → check that this version of Herdr is supported, or set multiplexer to { command = … }", tool, id, strings.TrimSpace(string(out)))
	}
	l.Env = append(slices.Clone(l.Env), "PATH="+os.Getenv("PATH"))
	if _, err := run(ctx, tool, "run", "--bg", "--no-parent", "--no-notify",
		"--label", "herdr.space="+space, "--label", "aw.lane="+l.Name, "--", "/bin/sh", "-c", l.shell()); err != nil {
		return "", err
	}
	return "a new herdr tab, " + l.Name, nil
}

// findLabel finds key's string value anywhere in a JSON reply, for reading a label from hc get
// whatever object holds it.
func findLabel(reply []byte, key string) string {
	var v any
	if json.Unmarshal(reply, &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case map[string]any:
			if s, ok := t[key].(string); ok && s != "" {
				return s
			}
			for _, k := range slices.Sorted(maps.Keys(t)) {
				if s := walk(t[k]); s != "" {
					return s
				}
			}
		case []any:
			for _, e := range t {
				if s := walk(e); s != "" {
					return s
				}
			}
		}
		return ""
	}
	return walk(v)
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
