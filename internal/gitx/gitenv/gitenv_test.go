package gitenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLocalCoversInstalledGit(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Skipf("git rev-parse --local-env-vars: %v", err)
	}
	for _, name := range strings.Fields(string(out)) {
		if !slices.Contains(Local, name) {
			t.Errorf("Git names %s as repository-local, and Local does not", name)
		}
	}
}

func TestWithout(t *testing.T) {
	env := []string{"PATH=/bin", "GIT_DIR=/r/.git/worktrees/03", "GIT_INDEX_FILE=/r/.git/index", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c", "GIT_EXEC_PATH=/usr/lib/git-core", "GIT_DIRTY=keep"}
	got := Without(env)
	want := []string{"PATH=/bin", "GIT_EXEC_PATH=/usr/lib/git-core", "GIT_DIRTY=keep"}
	if !slices.Equal(got, want) {
		t.Fatalf("Without = %q, want %q", got, want)
	}
}

func TestForWorktreeHook(t *testing.T) {
	env := []string{"PATH=/bin", "GIT_DIR=/r/.git/worktrees/03", "GIT_INDEX_FILE=/r/.git/worktrees/03/index.lock"}

	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /r/.git/worktrees/03\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := ForWorktreeHook(env, linked), []string{"PATH=/bin", "GIT_INDEX_FILE=/r/.git/worktrees/03/index.lock"}; !slices.Equal(got, want) {
		t.Errorf("in a linked worktree: %q, want %q", got, want)
	}

	main := t.TempDir()
	if err := os.Mkdir(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ForWorktreeHook(env, main); !slices.Equal(got, env) {
		t.Errorf("in a main worktree: %q, want it unchanged", got)
	}
	if got := ForWorktreeHook(env, t.TempDir()); !slices.Equal(got, env) {
		t.Errorf("in a Git directory: %q, want it unchanged", got)
	}
}
