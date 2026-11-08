package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Main(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionHuman(t *testing.T) {
	code, out, errOut := runCLI(t, "version")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	want := fmt.Sprintf("aw dev (config schema %d, state schema %d)\n", config.SchemaVersion, state.SchemaVersion)
	if out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, errOut := runCLI(t, "version", "--json")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	want := map[string]any{"version": "dev", "config_schema": float64(config.SchemaVersion), "state_schema": float64(state.SchemaVersion)}
	if len(got) != len(want) {
		t.Fatalf("got keys %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
}

func TestVersionSetAtLinkTime(t *testing.T) {
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })

	_, out, _ := runCLI(t, "version")
	if !strings.HasPrefix(out, "aw 1.2.3 ") {
		t.Fatalf("stdout %q does not report the linked version", out)
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	cases := map[string][]string{
		"unknown command":   {"no-such-command"},
		"unknown flag":      {"version", "--no-such-flag"},
		"unexpected arg":    {"version", "extra"},
		"unknown root flag": {"--no-such-flag"},
		"bad flag value":    {"version", "--json=maybe"},
		"arg on bare aw":    {"stray"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := runCLI(t, args...)
			if code != ExitUsage {
				t.Fatalf("exit %d, want %d (stderr %q)", code, ExitUsage, errOut)
			}
			if out != "" {
				t.Errorf("usage error wrote to stdout: %q", out)
			}
			if !strings.HasPrefix(errOut, "aw: ") {
				t.Errorf("stderr %q does not start with the error", errOut)
			}
		})
	}
}

func TestCommandErrorExit1(t *testing.T) {
	var out, errOut bytes.Buffer
	root := newRoot(&out, &errOut)
	root.AddCommand(&cobra.Command{
		Use: "fail",
		RunE: run(func(*cobra.Command, []string) error {
			return errors.New("it broke → aw doctor")
		}),
	})
	root.SetArgs([]string{"fail"})
	if code := exitCode(root.Execute()); code != ExitError {
		t.Fatalf("exit %d for a command error, want %d", code, ExitError)
	}
	if code := exitCode(nil); code != ExitOK {
		t.Fatalf("exit %d for success, want %d", code, ExitOK)
	}
}

func TestHelpExits0(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}, {"version", "--help"}} {
		code, out, errOut := runCLI(t, args...)
		if code != ExitOK {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: no usage on stdout: %q", args, out)
		}
	}
}
