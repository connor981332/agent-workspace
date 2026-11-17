// Package gitenv keeps Git's repository-local environment from reaching commands it was not meant
// for. Git exports GIT_DIR, GIT_INDEX_FILE and others to the hooks it runs, so that Git commands in a
// hook find the repository. A process that inherits them and then runs Git somewhere else, such as a
// package manager cloning into its cache, works on the wrong repository.
package gitenv

import (
	"os"
	"path/filepath"
	"strings"
)

// Local are the variables `git rev-parse --local-env-vars` names (Git 2.56). A test checks this list
// against the installed Git.
var Local = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE",
	"GIT_COMMON_DIR",
}

// Without returns env without Git's repository-local variables, for a command aw starts itself in a
// directory of its choosing: whatever repository those variables named is not that command's.
// GIT_CONFIG_COUNT's numbered GIT_CONFIG_KEY_n and GIT_CONFIG_VALUE_n go with it.
func Without(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if isLocal(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isLocal(name string) bool {
	for _, l := range Local {
		if name == l {
			return true
		}
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// ForWorktreeHook returns env for a team hook that Git started in a linked worktree's root (dir). Git
// sets GIT_DIR there, as an absolute path, although it does not in the main worktree, so a hook that
// works in the owner checkout can fail in a lane. Without GIT_DIR, Git finds the same repository from
// the worktree's .git file. Everything else Git set, such as a commit's temporary GIT_INDEX_FILE, is
// what the hook is meant to see, and stays.
func ForWorktreeHook(env []string, dir string) []string {
	if info, err := os.Lstat(filepath.Join(dir, ".git")); err != nil || info.IsDir() {
		return env // not a linked worktree's root: a main worktree, or a Git directory (receive-pack)
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
