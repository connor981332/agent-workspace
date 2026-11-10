package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowRehearse(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(ClaudeFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Bash(ls)"]},"sandbox":{"on":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if err := AllowRehearse(root, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bash(bin/aw rehearse)", "Bash(ls)", `"sandbox"`} {
		if !strings.Contains(read(), want) {
			t.Errorf("missing %s in %s", want, read())
		}
	}
	once := read()
	if err := AllowRehearse(root, true); err != nil || read() != once {
		t.Errorf("not idempotent: %v\n%s", err, read())
	}
	if err := AllowRehearse(root, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "rehearse") || !strings.Contains(got, "Bash(ls)") {
		t.Errorf("after removing: %s", got)
	}
}

func TestInstallAddsTheLanesWorktrees(t *testing.T) {
	root := t.TempDir()
	worktree, gitDir := "/w/api/.worktrees/02", "/w/api/.git"
	if _, err := Install(root, []string{gitDir, worktree}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ClaudeFile)))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	dirs := text[strings.Index(text, "additionalDirectories"):strings.Index(text, `"sandbox"`)]
	if !strings.Contains(dirs, worktree) || strings.Contains(dirs, gitDir) {
		t.Errorf("additionalDirectories are wrong:\n%s", text)
	}
}
