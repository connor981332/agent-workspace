// Package workspace finds the owner checkout and the lane root from a directory, and the paths
// that hang off them. It reads files only: no Git subprocess, so it is safe on the hot path.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is where a command runs.
type Workspace struct {
	// Owner is the owner checkout: the human's checkout of the workspace repository.
	Owner string
	// LaneRoot is the lane root the command runs in, or "" in the owner checkout.
	LaneRoot string
}

// NotFoundError means no workspace contains the directory.
type NotFoundError struct{ Dir string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s is not inside an Agent Workspace (no workspace.toml in it or above it) → cd into a workspace, or create one with aw init", e.Dir)
}

// OwnerPointer is the gitignored file in a lane root that names the owner checkout.
const OwnerPointer = ".aw/owner"

// Find walks up from dir. A directory with .aw/owner is a lane root, and the file names the owner
// checkout. A directory with workspace.toml is the owner checkout, unless the walk came through a
// project's lane worktree, <project>/.worktrees/NN, reached by its real path rather than through
// the lane root's link: that is lane NN, when its lane root points back at this owner checkout.
func Find(dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var lanes []string // NN of each <project>/.worktrees/NN passed on the way up
	for d := abs; ; d = filepath.Dir(d) {
		if filepath.Base(filepath.Dir(d)) == ".worktrees" {
			lanes = append(lanes, filepath.Base(d))
		}
		if data, err := os.ReadFile(filepath.Join(d, OwnerPointer)); err == nil {
			owner := strings.TrimSpace(string(data))
			if !filepath.IsAbs(owner) {
				return nil, fmt.Errorf("%s: %q is not an absolute path → recreate the lane, or write the owner checkout's absolute path there",
					filepath.Join(d, OwnerPointer), owner)
			}
			if _, err := os.Stat(filepath.Join(owner, "workspace.toml")); err != nil {
				return nil, fmt.Errorf("%s points at %s, which has no workspace.toml → check that the owner checkout has not moved",
					filepath.Join(d, OwnerPointer), owner)
			}
			return &Workspace{Owner: owner, LaneRoot: d}, nil
		}
		if _, err := os.Stat(filepath.Join(d, "workspace.toml")); err == nil {
			for i := len(lanes) - 1; i >= 0; i-- {
				root := filepath.Join(d, ".worktrees", lanes[i])
				data, err := os.ReadFile(filepath.Join(root, OwnerPointer))
				if err != nil {
					continue
				}
				// The pointer may name the owner by another path to it (a symlinked parent).
				pointed, err1 := os.Stat(strings.TrimSpace(string(data)))
				here, err2 := os.Stat(d)
				if err1 == nil && err2 == nil && os.SameFile(pointed, here) {
					return &Workspace{Owner: strings.TrimSpace(string(data)), LaneRoot: filepath.Join(strings.TrimSpace(string(data)), ".worktrees", lanes[i])}, nil
				}
			}
			return &Workspace{Owner: d}, nil
		}
		if parent := filepath.Dir(d); parent == d {
			return nil, &NotFoundError{Dir: abs}
		}
	}
}

// GitCommonDir returns the common Git directory of the repository whose working tree is dir,
// reading .git and commondir as Git does.
func GitCommonDir(dir string) (string, error) {
	dotgit := filepath.Join(dir, ".git")
	info, err := os.Lstat(dotgit)
	if err != nil {
		return "", fmt.Errorf("%s is not a Git repository (no .git) → run git init there, or clone the workspace again", dir)
	}
	if info.IsDir() {
		return dotgit, nil
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", err
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return "", fmt.Errorf("%s is not a gitdir file", dotgit)
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(gitdir), nil
	} else if err != nil {
		return "", err
	}
	c := strings.TrimSpace(string(common))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitdir, c)
	}
	return filepath.Clean(c), nil
}

// StateDir is <workspace git dir>/aw, shared by every worktree of the workspace repository.
func (w *Workspace) StateDir() (string, error) {
	common, err := GitCommonDir(w.Owner)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "aw"), nil
}
