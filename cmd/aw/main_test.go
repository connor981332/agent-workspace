package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildAW builds the real binary, so the tests see what a user runs, including os.Exit codes.
func buildAW(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "aw")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func runAW(t *testing.T, bin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %v: %v", args, err)
	}
	return code, out.String(), errOut.String()
}

func TestBinary(t *testing.T) {
	bin := buildAW(t)

	t.Run("version --json", func(t *testing.T) {
		code, out, errOut := runAW(t, bin, "version", "--json")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		var v struct {
			Version      *string `json:"version"`
			ConfigSchema *int    `json:"config_schema"`
			StateSchema  *int    `json:"state_schema"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if v.Version == nil || v.ConfigSchema == nil || v.StateSchema == nil {
			t.Fatalf("missing fields in %s", out)
		}
	})

	t.Run("unknown command exits 2", func(t *testing.T) {
		if code, _, _ := runAW(t, bin, "no-such-command"); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
	})

	// The hot-path commands are dispatched before the command tree exists, so cobra's flag parsing
	// and help never see them. Outside a workspace both refuse (fail closed) with exit 2.
	for _, name := range []string{"hook", "git-hook"} {
		t.Run(name+" bypasses the command tree", func(t *testing.T) {
			cmd := exec.Command(bin, name, "claude", "pre-tool-use", "--help")
			cmd.Dir = t.TempDir()
			cmd.Stdin = strings.NewReader("")
			var out, errOut strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errOut
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("%v, want exit 2", err)
			}
			if out.String() != "" || strings.Contains(errOut.String(), "Usage:") {
				t.Fatalf("the command tree handled %q: stdout %q, stderr %q", name, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), "refused") {
				t.Fatalf("stderr %q", errOut.String())
			}
		})
	}
}
