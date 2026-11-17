package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/gitx/gitenv"
)

// Prerequisites (plans/dogfood-0.3.md, decision 8): what the machine must have before a project's
// setup can work. A check runs once before a setup that needs it, and its result is kept for every
// lane: a prerequisite that was met for a few minutes, one that was not for a few seconds, so that
// a setup waiting on the human notices soon after it is fixed.
const (
	prereqMetFor    = 5 * time.Minute
	prereqUnmetFor  = 10 * time.Second
	prereqTimeout   = 20 * time.Second
	prereqPoll      = 5 * time.Second
	prereqWaitLimit = 30 * time.Minute
	prereqFile      = "prerequisites.json"
)

// PrereqResult is one prerequisite's last check.
type PrereqResult struct {
	Name    string `json:"name"`
	Met     bool   `json:"met"`
	Checked string `json:"checked"`          // RFC 3339
	Output  string `json:"output,omitempty"` // the last line the check printed, when it failed
	Fix     string `json:"fix,omitempty"`
	Message string `json:"message,omitempty"`
}

// Fresh reports whether the result can stand in for a check now.
func (r PrereqResult) Fresh(now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, r.Checked)
	if err != nil {
		return false
	}
	if r.Met {
		return now.Sub(t) < prereqMetFor
	}
	return now.Sub(t) < prereqUnmetFor
}

// Unmet says what is missing and what the human does about it, in one line.
func (r PrereqResult) Unmet() string {
	s := fmt.Sprintf("prerequisite %s is not met", r.Name)
	if r.Message != "" {
		s += " (" + r.Message + ")"
	}
	if r.Fix != "" {
		s += " → " + r.Fix
	}
	return s
}

// LastPrereqs reads the results kept in the workspace's state directory, without checking anything.
func LastPrereqs(stateDir string) map[string]PrereqResult {
	out := map[string]PrereqResult{}
	data, err := os.ReadFile(filepath.Join(stateDir, prereqFile))
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func savePrereq(stateDir string, r PrereqResult) {
	all := LastPrereqs(stateDir)
	all[r.Name] = r
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(stateDir, prereqFile+".tmp")
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(stateDir, prereqFile)) // best effort: the next setup checks again
	}
}

// CheckPrereq runs one prerequisite's check from the owner checkout and keeps the result.
func CheckPrereq(ctx context.Context, owner, stateDir, name string, p *config.Prerequisite) PrereqResult {
	r := PrereqResult{Name: name, Fix: p.Fix, Message: p.Message}
	ctx, cancel := context.WithTimeout(ctx, prereqTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", p.Check)
	cmd.Dir = owner
	cmd.Env = gitenv.Without(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r.Met = err == nil
	if !r.Met {
		r.Output = lastLine(out.String())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.Output = fmt.Sprintf("the check did not finish within %s", prereqTimeout)
		}
	}
	r.Checked = time.Now().UTC().Format(time.RFC3339Nano)
	if stateDir != "" {
		savePrereq(stateDir, r)
	}
	return r
}

// Prereq returns a prerequisite's result: the kept one while it is fresh, else a new check.
func Prereq(ctx context.Context, owner, stateDir, name string, p *config.Prerequisite) PrereqResult {
	if stateDir == "" {
		return CheckPrereq(ctx, owner, "", name, p)
	}
	if r, ok := LastPrereqs(stateDir)[name]; ok && r.Fresh(time.Now()) {
		r.Fix, r.Message = p.Fix, p.Message
		return r
	}
	return CheckPrereq(ctx, owner, stateDir, name, p)
}

// ProjectPrereqs are the prerequisites a project names, in order.
func ProjectPrereqs(c *config.Config, project string) []string {
	p := c.Projects[project]
	if p == nil {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(p.Prerequisites), func(n string) bool { return c.Prerequisites[n] == nil })
}

// awaitPrereqs holds a project's setup until its prerequisites are met (decision 8): the step shows
// as waiting on the human, keeps its place, and is checked again every few seconds. It reports false
// when the wait ends without them, as when the human stops it or after half an hour.
func (r *Runner) awaitPrereqs(ctx context.Context, s Step, wait func(detail string)) (bool, string) {
	names := ProjectPrereqs(r.Cfg, s.Project)
	if len(names) == 0 || r.Owner == "" {
		return true, ""
	}
	deadline := time.Now().Add(prereqWaitLimit)
	said := ""
	for {
		var unmet []string
		for _, n := range names {
			if res := Prereq(ctx, r.Owner, r.CacheDir, n, r.Cfg.Prerequisites[n]); !res.Met {
				unmet = append(unmet, res.Unmet())
			}
		}
		if len(unmet) == 0 {
			return true, ""
		}
		detail := "waiting on you: " + strings.Join(unmet, "; ")
		if detail != said {
			said = detail
			wait(detail)
		}
		if time.Now().After(deadline) {
			return false, strings.Join(unmet, "; ") + ", for half an hour → meet it, then aw setup " + r.Lane.Name
		}
		select {
		case <-ctx.Done():
			return false, strings.Join(unmet, "; ")
		case <-time.After(prereqPoll):
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(line); len(r) > 200 {
		line = string(r[:200]) + "…"
	}
	return line
}
