package lanes

import (
	"strings"
	"testing"

	"github.com/connor981332/agent-workspace/internal/state"
)

// Every session that holds a lane is named, not only the first (#126).
func TestAttachedLineNamesEverySession(t *testing.T) {
	holders := []state.Session{
		{HarnessSession: "4e7e27aa", Harness: "claude"},
		{HarnessSession: "new-6d4a5b26", Harness: "shell"},
	}
	if got, want := attachedLine(holders), "2 sessions are attached: 4e7e27aa (claude), new-6d4a5b26 (shell)"; got != want {
		t.Errorf("attachedLine = %q, want %q", got, want)
	}
	if got, want := attachedLine(holders[:1]), "session 4e7e27aa (claude) is attached"; got != want {
		t.Errorf("attachedLine = %q, want %q", got, want)
	}
	steps := endSteps(holders)
	if len(steps) != 2 || !strings.Contains(steps[1], "new-6d4a5b26 (shell): exit that shell, or run aw detach in it") {
		t.Errorf("endSteps = %q", steps)
	}
}
