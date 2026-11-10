// Package launch knows how to start each harness on a session: its resume flag and how it takes a
// first prompt. It runs nothing, and it is kept apart from package harness, which is on the hot path.
package launch

import (
	"strconv"
	"strings"
)

// ResumeArgs are the arguments that resume a session by ID, and ok is whether aw knows them for
// this harness (measured from each harness's --help: claude 2.1.319, codex 0.167.0, grok 1.0.77,
// opencode 2.2.6).
func ResumeArgs(harness, id string) (args []string, ok bool) {
	switch harness {
	case "claude", "grok":
		return []string{"--resume", id}, true
	case "codex":
		return []string{"resume", id}, true
	case "opencode":
		return []string{"--session", id}, true
	}
	return nil, false
}

// PromptArgs are the arguments that start a new interactive session with a first prompt.
func PromptArgs(harness, prompt string) []string {
	if harness == "opencode" {
		return []string{"--prompt", prompt}
	}
	return []string{prompt}
}

// SandboxArgs are the arguments a harness needs to write aw's state from inside its sandbox. Codex
// reads a project's .codex/config.toml only once that project is trusted, so aw says it directly.
func SandboxArgs(harness string, writable []string) []string {
	if harness != "codex" || len(writable) == 0 {
		return nil
	}
	quoted := make([]string, len(writable))
	for i, w := range writable {
		quoted[i] = strconv.Quote(w)
	}
	return []string{"-c", "sandbox_workspace_write.writable_roots=[" + strings.Join(quoted, ",") + "]"}
}

// DirArgs tell a harness about the directories a lane's links lead to, so that it does not ask about
// each path through them. They go after everything else, because the option takes several values.
func DirArgs(harness string, writable []string) []string {
	if harness != "claude" {
		return nil
	}
	var out []string
	for _, w := range writable {
		if !strings.HasSuffix(w, "/.git") {
			out = append(out, "--add-dir", w)
		}
	}
	return out
}
