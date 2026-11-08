package cmds

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

// snapshot maps a description of one piece of the owner checkout to its hash or value.
type snapshot map[string]string

// takeSnapshot records, for every repository in the owner checkout: its files outside .git and
// .worktrees (lanes live there), the index, HEAD, every ref, the stash and .git/config.
func takeSnapshot(ts *testscript.TestScript) snapshot {
	st := get(ts)
	snap := snapshot{}
	for _, repo := range st.repos {
		dir := filepath.Join(st.owner, repo)
		prefix := repo + ": "

		// Nested project repositories are snapshotted on their own.
		var skip []string
		if repo == "." {
			for _, r := range st.repos {
				if r != "." {
					skip = append(skip, r)
				}
			}
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			if rel == "." {
				return nil
			}
			if rel == ".git" || rel == ".worktrees" || slices.Contains(skip, rel) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			key := prefix + "file " + filepath.ToSlash(rel)
			switch {
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				snap[key] = "symlink -> " + target
			case d.IsDir():
				snap[key] = "dir"
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(data)
				snap[key] = info.Mode().Perm().String() + " " + hex.EncodeToString(sum[:])
			}
			return nil
		})
		ts.Check(err)

		sum := sha256.Sum256([]byte(git(ts, dir, "ls-files", "--stage", "-z")))
		snap[prefix+"index"] = hex.EncodeToString(sum[:])
		snap[prefix+"HEAD"] = strings.TrimSpace(gitMaybe(ts, dir, "symbolic-ref", "-q", "HEAD")) +
			" " + strings.TrimSpace(gitMaybe(ts, dir, "rev-parse", "-q", "--verify", "HEAD"))
		for _, line := range strings.Split(strings.TrimSpace(git(ts, dir, "for-each-ref", "--format=%(refname) %(objectname)")), "\n") {
			if ref, oid, ok := strings.Cut(line, " "); ok {
				snap[prefix+"ref "+ref] = oid
			}
		}
		snap[prefix+"ref refs/stash"] = strings.TrimSpace(gitMaybe(ts, dir, "rev-parse", "-q", "--verify", "refs/stash"))
		gitDir := strings.TrimSpace(git(ts, dir, "rev-parse", "--absolute-git-dir"))
		config, err := os.ReadFile(filepath.Join(gitDir, "config"))
		ts.Check(err)
		sum = sha256.Sum256(config)
		snap[prefix+".git/config"] = hex.EncodeToString(sum[:])
	}
	return snap
}

// gitMaybe is git for queries whose failure means "absent".
func gitMaybe(ts *testscript.TestScript, dir string, args ...string) string {
	out, err := gitRun(ts, dir, args...)
	if err != nil {
		return ""
	}
	return out
}

// diff lists what changed from before to after. Refs that appeared are lanes' branches and aw's own
// refs, which the owner checkout does not see, so only refs that existed before are compared.
func (before snapshot) diff(after snapshot) []string {
	var changes []string
	for k, v := range before {
		if a, ok := after[k]; !ok {
			changes = append(changes, "removed: "+k)
		} else if a != v {
			changes = append(changes, "changed: "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; ok {
			continue
		}
		if strings.Contains(k, ": ref ") && !strings.HasSuffix(k, " refs/stash") {
			continue
		}
		changes = append(changes, "added: "+k)
	}
	slices.Sort(changes)
	return changes
}

func needWorkspace(ts *testscript.TestScript, cmd string) *state {
	st := get(ts)
	if st.owner == "" {
		ts.Fatalf("%s: no workspace; run the workspace command first", cmd)
	}
	return st
}

// owner-snapshot: record the owner checkout now (for example after the human's own changes).
func cmdOwnerSnapshot(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: owner-snapshot")
	}
	st := needWorkspace(ts, "owner-snapshot")
	st.snap = takeSnapshot(ts)
}

// owner-unchanged: the owner checkout is as the last snapshot recorded it.
func cmdOwnerUnchanged(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 0 {
		ts.Fatalf("usage: [!] owner-unchanged")
	}
	st := needWorkspace(ts, "owner-unchanged")
	changes := st.snap.diff(takeSnapshot(ts))
	switch {
	case neg && len(changes) == 0:
		ts.Fatalf("the owner checkout is unchanged, but the scenario expected a change")
	case !neg && len(changes) > 0:
		ts.Fatalf("the owner checkout changed:\n  %s", strings.Join(changes, "\n  "))
	}
}

// owner-may-change: this scenario changes the owner checkout on purpose (for example, the human's
// own commits), so the automatic check at the end is skipped.
func cmdOwnerMayChange(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: owner-may-change")
	}
	get(ts).mayChange = true
}

func cmdOwnerCheckFinal(ts *testscript.TestScript, neg bool, args []string) {
	st := get(ts)
	if st.owner == "" || st.mayChange {
		return
	}
	if changes := st.snap.diff(takeSnapshot(ts)); len(changes) > 0 {
		ts.Fatalf("the owner checkout changed during this scenario (declare owner-may-change if that is intended):\n  %s",
			strings.Join(changes, "\n  "))
	}
}
