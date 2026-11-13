package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestExcludePreservesConcurrentAppends(t *testing.T) {
	for round := 0; round < 8; round++ {
		root := t.TempDir()
		path := filepath.Join(root, ".git", "info", "exclude")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# user rules\n/keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 64)
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			pattern := fmt.Sprintf("/materialized-%d\n", i)
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
				if err == nil {
					_, err = f.WriteString(pattern)
					closeErr := f.Close()
					if err == nil {
						err = closeErr
					}
				}
				errs <- err
			}()
			go func() { defer wg.Done(); <-start; errs <- Exclude(root) }()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		wantPatterns := []string{"/keep", "/" + ClaudeFile, "/" + CodexFile}
		for i := 0; i < 32; i++ {
			wantPatterns = append(wantPatterns, fmt.Sprintf("/materialized-%d", i))
		}
		for _, want := range wantPatterns {
			found := false
			for _, line := range lines {
				found = found || line == want
			}
			if !found {
				t.Fatalf("round %d: concurrent exclude update lost %s", round, want)
			}
		}
	}
}
