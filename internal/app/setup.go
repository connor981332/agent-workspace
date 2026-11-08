package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/harness"
)

// HooksResult says what aw setup did to one repository.
type HooksResult struct {
	Repo      string `json:"repo"`
	Installed bool   `json:"installed"`         // false: it was already in place
	TeamHooks string `json:"team_hooks"`        // the hooks directory the dispatcher runs afterwards
	Missing   bool   `json:"missing,omitempty"` // the project is not cloned yet
}

// InstallGitHooks points every repository's core.hooksPath at the dispatcher. It is safe to rerun.
func InstallGitHooks(ctx context.Context, owner string, cfg *config.Config) ([]HooksResult, error) {
	repos := map[string]bool{".": true}
	for _, p := range cfg.Projects {
		repos[p.Directory] = true
	}
	var names []string
	for r := range repos {
		names = append(names, r)
	}
	sort.Strings(names)
	var out []HooksResult
	for _, rel := range names {
		dir := filepath.Join(owner, rel)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			out = append(out, HooksResult{Repo: rel, Missing: true})
			continue
		}
		r, err := installHooks(ctx, owner, dir)
		if err != nil {
			return out, err
		}
		r.Repo = rel
		out = append(out, r)
	}
	return out, nil
}

func installHooks(ctx context.Context, owner, dir string) (HooksResult, error) {
	var res HooksResult
	g := run.In(dir)
	common, err := g.Line(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return res, err
	}
	hooks := filepath.Join(common, "aw", "hooks")
	current, _ := g.Line(ctx, "config", "--local", "--get", "core.hooksPath")
	team, _ := g.Line(ctx, "config", "--local", "--get", "aw.teamHooksPath")
	if current != hooks {
		team = current
		if team == "" {
			team = filepath.Join(common, "hooks")
		}
		res.Installed = true
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return res, err
	}
	for _, name := range harness.GitHookNames {
		data := []byte(dispatcher(owner, team, name))
		path := filepath.Join(hooks, name)
		if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
			continue
		}
		res.Installed = true
		if err := os.WriteFile(path+".tmp", data, 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return res, err
		}
	}
	// The team's path is recorded first: if this stops halfway, a rerun still knows it.
	if _, err := g.Run(ctx, "config", "--local", "aw.teamHooksPath", team); err != nil {
		return res, err
	}
	if _, err := g.Run(ctx, "config", "--local", "core.hooksPath", hooks); err != nil {
		return res, err
	}
	res.TeamHooks = team
	return res, nil
}

// dispatcher is one hook's script. In the owner checkout (GIT_DIR unset, .git a directory; a lane
// worktree's .git is a file) it never starts aw, so plain Git there works even without aw.
func dispatcher(owner, team, name string) string {
	return fmt.Sprintf(`#!/bin/sh
# Managed by aw setup: do not edit. aw's check, then the team's hook.
AW_TEAM_HOOKS=%s
export AW_TEAM_HOOKS
if [ -z "${GIT_DIR:-}" ] && [ -d .git ]; then
	if [ -x "$AW_TEAM_HOOKS/%s" ]; then exec "$AW_TEAM_HOOKS/%s" "$@"; fi
	exit 0
fi
exec %s git-hook %s "$@"
`, shQuote(team), name, name, shQuote(filepath.Join(owner, "bin", "aw")), name)
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RegisterWorkspace records this workspace on the machine, so that aw doctor can compare it with the
// others there (S15). It is safe to rerun.
func RegisterWorkspace(owner string) error {
	path := config.RegistryFile()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == owner {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, owner)
	return err
}
