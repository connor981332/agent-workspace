package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlainOutput(t *testing.T) {
	var b bytes.Buffer
	o := Plain(&b)
	o.Heading("Setting up")
	s := o.Step("web: installing dependencies")
	s.Detail("resolving packages")
	s.Done("installed")
	o.Step("api: installing dependencies").Fail("exit status 1")
	o.Warn("snui-web has no smoke command")
	o.Summary("1 project did not install: api → aw setup again once it is fixed")
	o.Close()
	want := `Setting up
web: installing dependencies: installed
error: api: installing dependencies: exit status 1
warning: snui-web has no smoke command

1 project did not install: api → aw setup again once it is fixed
`
	if got := b.String(); got != want {
		t.Errorf("plain output:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(b.String(), "\033") || strings.Contains(b.String(), "resolving") {
		t.Error("plain output has escape codes or a step's detail")
	}
}

func TestTableFitsWidth(t *testing.T) {
	var b bytes.Buffer
	o := Plain(&b)
	o.width = 40
	o.Table([][]string{
		{"01", "checkout-tax", "working", "fix checkout tax rounding in the cart and the invoice"},
		{"12", "x", "ready", "short"},
	}, 3)
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if n := len([]rune(line)); n > 40 {
			t.Errorf("%q is %d wide, more than 40", line, n)
		}
	}
	if !strings.HasPrefix(b.String(), "01  checkout-tax  working  fix checkout") || !strings.Contains(b.String(), "…") {
		t.Errorf("table:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "\n12  x             ready    short\n") {
		t.Errorf("columns are not aligned:\n%s", b.String())
	}
}

func TestLogKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	var b bytes.Buffer
	o := Plain(&b)
	path := o.LogTo(dir, "setup", 2, 0)
	s := o.Step("web: installing")
	s.Detail("line one of the install")
	s.Fail("exit status 3")
	o.Close()
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "line one of the install") || !strings.Contains(string(data), "FAILED web: installing") {
		t.Fatalf("log %s: %q (%v)", path, data, err)
	}
	for i := 0; i < 3; i++ {
		time.Sleep(10 * time.Millisecond)
		Plain(&b).LogTo(dir, "setup", 2, 0)
	}
	if logs, _ := filepath.Glob(filepath.Join(dir, "*.log")); len(logs) != 2 {
		t.Errorf("%d logs kept, want 2", len(logs))
	}
}

func TestFitVisible(t *testing.T) {
	s := green + "✓ " + reset + "web: installed everything"
	if got := fitVisible(s, 10); strings.Count(got, "…") != 1 || len([]rune(stripEsc(got))) != 10 {
		t.Errorf("fitVisible = %q (%d visible)", got, len([]rune(stripEsc(got))))
	}
}

func stripEsc(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\033':
			in = true
		case in:
			if r >= '@' && r <= '~' && r != '[' {
				in = false
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
