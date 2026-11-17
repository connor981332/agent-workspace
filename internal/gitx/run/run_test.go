package run

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLongFailureKeepsItsWholeOutput(t *testing.T) {
	var lines []string
	for i := 1; i <= 41+tailLines; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	e := &Error{Dir: "/r", Args: []string{"switch", "x"}, Stderr: strings.Join(lines, "\n"), Err: fmt.Errorf("exit status 1")}

	LogDir = ""
	if keepLog(e) != "" {
		t.Fatal("with no LogDir, nothing is kept")
	}
	if msg := e.Error(); !strings.Contains(msg, "(41 earlier lines not shown)") {
		t.Errorf("without a log: %q", msg)
	}

	LogDir = t.TempDir()
	t.Cleanup(func() { LogDir = "" })
	e.Log = keepLog(e)
	if e.Log == "" {
		t.Fatal("a long failure was not kept")
	}
	data, err := os.ReadFile(e.Log)
	if err != nil || !strings.Contains(string(data), "line 1\n") || !strings.HasPrefix(string(data), "git switch x\n") {
		t.Fatalf("the log holds %q (%v)", data, err)
	}
	if msg := e.Error(); !strings.Contains(msg, "all of it is in "+e.Log) || strings.Contains(msg, "line 1\n") {
		t.Errorf("message: %q", msg)
	}

	short := &Error{Dir: "/r", Args: []string{"x"}, Stderr: "one line", Err: fmt.Errorf("exit status 1")}
	if keepLog(short) != "" {
		t.Error("a failure shown whole needs no log")
	}
}
