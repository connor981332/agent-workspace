package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "it's a workspace")
	write(t, filepath.Join(owner, "workspace.toml"), "")
	lane := filepath.Join(owner, ".worktrees", "03")
	write(t, filepath.Join(lane, ".aw", "owner"), owner+"\n")
	write(t, filepath.Join(lane, "workspace.toml"), "") // the lane's own copy is not the owner's
	write(t, filepath.Join(owner, "web", "src", "x"), "")
	webLane := filepath.Join(owner, "web", ".worktrees", "03", "src")
	write(t, filepath.Join(webLane, "x"), "")
	stray := filepath.Join(owner, "web", ".worktrees", "07")
	write(t, filepath.Join(stray, "x"), "") // no lane root 07: the owner checkout

	cases := map[string]Workspace{
		owner:                              {Owner: owner},
		filepath.Join(owner, "web", "src"): {Owner: owner},
		lane:                               {Owner: owner, LaneRoot: lane},
		webLane:                            {Owner: owner, LaneRoot: lane}, // a project worktree by its real path
		stray:                              {Owner: owner},
	}
	for dir, want := range cases {
		got, err := Find(dir)
		if err != nil || *got != want {
			t.Errorf("Find(%s) = %+v, %v; want %+v", dir, got, err, want)
		}
	}
	var nf *NotFoundError
	if _, err := Find(root); !errors.As(err, &nf) {
		t.Errorf("outside a workspace: %v", err)
	}
	write(t, filepath.Join(lane, ".aw", "owner"), "relative/path\n")
	if _, err := Find(lane); err == nil {
		t.Errorf("a relative owner pointer was accepted")
	}
}

func TestGitCommonDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "worktrees", "03"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := GitCommonDir(root); err != nil || got != filepath.Join(root, ".git") {
		t.Errorf("owner: %s, %v", got, err)
	}
	lane := filepath.Join(root, ".worktrees", "03")
	write(t, filepath.Join(lane, ".git"), "gitdir: "+filepath.Join(root, ".git", "worktrees", "03")+"\n")
	write(t, filepath.Join(root, ".git", "worktrees", "03", "commondir"), "../..\n")
	if got, err := GitCommonDir(lane); err != nil || got != filepath.Join(root, ".git") {
		t.Errorf("lane: %s, %v", got, err)
	}
	if _, err := GitCommonDir(t.TempDir()); err == nil {
		t.Errorf("no repository: no error")
	}
}

// A rehearsal needs both the marker and the place, so that a marker written into a real checkout
// does not make it one.
func TestIsRehearsal(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "shop")
	copied := filepath.Join(real, ".worktrees", RehearsalPrefix+"ab")
	for _, d := range []string{real, copied} {
		if err := os.MkdirAll(filepath.Join(d, ".aw"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, filepath.FromSlash(RehearsalMarker)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if IsRehearsal(real) {
		t.Error("a real checkout with the marker counts as a rehearsal")
	}
	if !IsRehearsal(copied) {
		t.Error("a rehearsal copy does not count as one")
	}
}
