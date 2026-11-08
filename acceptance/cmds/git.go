package cmds

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

// lane-commit <NN> [<project>/]<file> <message>
//
// Commits in a lane, as its agent would: writes <message> to the file in the lane's worktree (the
// lane root, or <project>'s worktree), then adds and commits it.
func cmdLaneCommit(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: lane-commit <NN> [<project>/]<file> <message>")
	}
	st := needWorkspace(ts, "lane-commit")
	dir := filepath.Join(st.owner, ".worktrees", args[0])
	file := args[1]
	if project, rest, ok := strings.Cut(file, "/"); ok && contains(st.repos, project) {
		dir, file = filepath.Join(st.owner, project, ".worktrees", args[0]), rest
	}
	path := filepath.Join(dir, file)
	ts.Check(os.MkdirAll(filepath.Dir(path), 0o755))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	ts.Check(err)
	_, err = f.WriteString(args[2] + "\n")
	ts.Check(err)
	ts.Check(f.Close())
	git(ts, dir, "add", "--", file)
	git(ts, dir, "commit", "-q", "-m", args[2])
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ownerRepo is a repository of the owner checkout: "." or a project directory.
func ownerRepo(ts *testscript.TestScript, st *state, repo string) string {
	if repo != "." && !contains(st.repos, repo) {
		ts.Fatalf("no repository %q in the workspace (have %v)", repo, st.repos)
	}
	return filepath.Join(st.owner, repo)
}

// git-merge <repo> <branch>, git-squash <repo> <branch>, git-cherry-pick <repo> <branch>
//
// The human merges a lane's branch into main in the owner checkout and pushes: a merge commit, a
// squash of every commit into one, or the commits cherry-picked one by one. The owner snapshot is
// taken again afterwards, because this is the human's own change.
func mergeCmd(how string) func(ts *testscript.TestScript, neg bool, args []string) {
	return func(ts *testscript.TestScript, neg bool, args []string) {
		if neg || len(args) != 2 {
			ts.Fatalf("usage: git-%s <repo> <branch>", how)
		}
		st := needWorkspace(ts, "git-"+how)
		dir := ownerRepo(ts, st, args[0])
		branch := args[1]
		switch how {
		case "merge":
			git(ts, dir, "merge", "-q", "--no-ff", "-m", "Merge "+branch, branch)
		case "squash":
			git(ts, dir, "merge", "-q", "--squash", branch)
			git(ts, dir, "commit", "-q", "-m", "Squash "+branch)
		case "cherry-pick":
			base := strings.TrimSpace(git(ts, dir, "merge-base", "HEAD", branch))
			git(ts, dir, "cherry-pick", base+".."+branch)
		}
		git(ts, dir, "push", "-q")
		st.snap = takeSnapshot(ts)
	}
}
