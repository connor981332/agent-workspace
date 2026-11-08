// Package launch knows how to start each harness on a session: its resume flag and how it takes a
// first prompt. It runs nothing, and it is kept apart from package harness, which is on the hot path.
package launch

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
