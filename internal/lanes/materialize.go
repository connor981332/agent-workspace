package lanes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// workspaceRoots are the paths in a workspace repository that a lane root needs: the launcher, the
// configuration, the instructions and each harness's hooks. aw setup writes them, and they work
// before anyone has committed them.
var workspaceRoots = []string{"bin/aw", "workspace.toml", "AGENTS.md", "CLAUDE.md", ".aw", ".claude", ".codex", ".grok", ".opencode", ".agents"}

// plainRoot reports whether the lane root cannot be a worktree of the workspace repository: there
// is none, or it has no commit yet.
func (m *Manager) plainRoot(ctx context.Context) bool {
	if !workspace.IsGitRepo(m.WS.Owner) {
		return true
	}
	return !ok(run.In(m.WS.Owner).OK(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"))
}

// CanReview reports whether a lane's work could go on a branch: the workspace repository, or the
// single project that is the workspace, has a commit. Otherwise onboarding works in the owner checkout.
func (m *Manager) CanReview(ctx context.Context) bool { return !m.plainRoot(ctx) }

// IsWorkspaceFile reports whether a path in the workspace repository is one aw init or aw setup
// writes, or a harness's own configuration beside them.
func IsWorkspaceFile(rel string) bool {
	if rel == ".gitignore" {
		return true
	}
	if neverCopied(rel) {
		return false
	}
	return slices.ContainsFunc(workspaceRoots, func(r string) bool { return rel == r || strings.HasPrefix(rel, r+"/") })
}

// neverCopied are aw's records for one checkout, and files each lane gets its own of.
func neverCopied(rel string) bool {
	return rel == workspace.OwnerPointer || rel == workspace.MaterializedPath || rel == workspace.LocalStateDir ||
		strings.HasPrefix(rel, workspace.LocalStateDir+"/") || slices.Contains(sandbox.Files, rel)
}

// materialize gives the lane root what it needs and its commit lacks, so lanes work before the
// workspace's files are committed, or when they are never in Git at all. It records what it copied,
// with hashes: an unchanged copy does not count as the lane's work, and a changed one does.
func (m *Manager) materialize(ctx context.Context, root string, plans []repoPlan) error {
	inGit := slices.IndexFunc(plans, func(p repoPlan) bool { return p.project == "" || p.rel == "." })
	var files []string
	var err error
	if inGit >= 0 {
		files, err = uncommitted(ctx, plans[inGit])
	} else {
		files, err = m.plainFiles()
	}
	if err != nil {
		return err
	}
	if m.AWHooksOnly {
		// A harness file the commit already has is not otherwise copied, and its hooks would still run.
		for _, rel := range sandbox.HarnessConfigs() {
			if !slices.Contains(files, rel) && sandbox.HasOtherHooks(filepath.Join(m.WS.Owner, filepath.FromSlash(rel))) {
				files = append(files, rel)
			}
		}
	}
	var list strings.Builder
	for _, rel := range files {
		src, dst := filepath.Join(m.WS.Owner, filepath.FromSlash(rel)), filepath.Join(root, filepath.FromSlash(rel))
		if inGit >= 0 {
			os.Remove(dst) // the commit's version of a file the owner checkout has changed
		}
		copied, err := copyOne(src, dst)
		if err != nil {
			return fmt.Errorf("copying %s into the lane: %w", rel, err)
		}
		if copied && m.AWHooksOnly && slices.Contains(sandbox.HarnessConfigs(), rel) {
			if err := sandbox.DropOtherHooks(dst); err != nil {
				return fmt.Errorf("turning off other hooks in %s: %w", rel, err)
			}
		}
		if copied {
			fmt.Fprintf(&list, "%s  %s\n", workspace.HashFile(dst), rel)
		}
	}
	if list.Len() == 0 {
		return nil
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(workspace.MaterializedPath)), []byte(list.String()), 0o644); err != nil {
		return err
	}
	if inGit < 0 {
		return nil
	}
	// A .gitignore that has not been committed cannot keep aw's own links out of the lane's status.
	excludes := []string{"/" + workspace.MaterializedPath, "/" + workspace.OwnerPointer, "/.worktrees/"}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		if d := m.Cfg.Projects[name].Directory; d != "." {
			excludes = append(excludes, "/"+d)
		}
	}
	common, err := workspace.GitCommonDir(plans[inGit].owner)
	if err != nil {
		return err
	}
	return addExcludes(filepath.Join(common, "info", "exclude"), excludes)
}

// uncommitted lists the workspace files the owner checkout has and the lane's commit does not, or
// that the owner has changed and not committed: aw setup edits files such as .claude/settings.json,
// and a lane needs its hooks. A file the owner committed is not copied: the lane starts from origin,
// which may have a newer version than an owner checkout that has not pulled.
func uncommitted(ctx context.Context, p repoPlan) ([]string, error) {
	g := run.In(p.owner)
	type listing struct {
		text string
		err  error
	}
	tree, changed := make(chan listing, 1), make(chan listing, 1)
	go func() { // at once, because every claim comes through here
		text, err := g.Run(ctx, "ls-tree", "-r", "-z", "--name-only", p.baseSHA)
		tree <- listing{text, err}
	}()
	go func() {
		text, err := g.Run(ctx, append([]string{"diff", "-z", "--name-only", "HEAD", "--"}, workspaceRoots...)...)
		changed <- listing{text, err}
	}()
	have, err := g.Run(ctx, append([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, workspaceRoots...)...)
	done, edits := <-tree, <-changed
	if err == nil {
		err = errors.Join(done.err, edits.err)
	}
	if err != nil {
		return nil, err
	}
	base := done.text
	committed := map[string]bool{}
	for _, f := range strings.Split(base, "\x00") {
		committed[f] = true
	}
	edited := map[string]bool{}
	for _, f := range strings.Split(edits.text, "\x00") {
		edited[f] = true
	}
	var out []string
	for _, f := range strings.Split(have, "\x00") {
		if f != "" && (!committed[f] || edited[f]) && !neverCopied(f) {
			if _, err := os.Lstat(filepath.Join(p.owner, filepath.FromSlash(f))); err == nil {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// plainFiles lists a workspace that is not in Git: everything but the projects and aw's own records.
func (m *Manager) plainFiles() ([]string, error) {
	skip := map[string]bool{".git": true, ".worktrees": true}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		skip[strings.SplitN(m.Cfg.Projects[name].Directory, "/", 2)[0]] = true
	}
	var out []string
	err := filepath.WalkDir(m.WS.Owner, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(m.WS.Owner, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && (skip[rel] || neverCopied(rel)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && !neverCopied(rel) && !skip[path.Base(rel)] {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// copyOne copies a file or a link, keeping its mode, and leaves a file that is already there.
func copyOne(src, dst string) (bool, error) {
	if _, err := os.Lstat(dst); err == nil {
		return false, nil
	}
	info, err := os.Lstat(src)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return false, err
		}
		return true, os.Symlink(target, dst)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(dst, data, info.Mode().Perm())
}

// removeMaterialized deletes the copies still as aw wrote them, and the list.
func removeMaterialized(worktree string) {
	unchanged := workspace.UnchangedMaterialized(worktree)
	for _, rel := range unchanged {
		os.Remove(filepath.Join(worktree, filepath.FromSlash(rel)))
	}
	os.Remove(filepath.Join(worktree, filepath.FromSlash(workspace.MaterializedPath)))
	if len(unchanged) == 0 {
		return
	}
	// A copy that replaced a tracked file goes back to the commit's version.
	g, ctx := run.In(worktree), context.Background()
	tracked, _ := g.Run(ctx, append([]string{"ls-files", "-z", "--"}, unchanged...)...)
	if files := strings.FieldsFunc(tracked, func(r rune) bool { return r == 0 }); len(files) > 0 {
		g.Run(ctx, append([]string{"checkout", "-q", "--"}, files...)...)
	}
}

// copyLocalFiles copies a project's declared local files into its new worktree: a clone where the
// file system can make one, so a large file costs nothing. A file the branch tracks is left alone.
func copyLocalFiles(ctx context.Context, p repoPlan) error {
	if len(p.localFiles) == 0 {
		return nil
	}
	var excludes []string
	for _, rel := range p.localFiles {
		src, dst := filepath.Join(p.owner, rel), filepath.Join(p.path, rel)
		if _, err := os.Lstat(src); err != nil {
			continue // the owner has no such file yet, and aw doctor says so
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := cloneFile(ctx, src, dst); err != nil {
			return fmt.Errorf("copying %s into the lane: %w", rel, err)
		}
		excludes = append(excludes, "/"+filepath.ToSlash(rel))
	}
	common, err := workspace.GitCommonDir(p.owner)
	if err != nil {
		return err
	}
	return addExcludes(filepath.Join(common, "info", "exclude"), excludes)
}

func cloneFile(ctx context.Context, src, dst string) error {
	clone := "--reflink=auto"
	if runtime.GOOS == "darwin" {
		clone = "-c"
	}
	if exec.CommandContext(ctx, "cp", clone, "-pR", src, dst).Run() == nil {
		return nil
	}
	os.RemoveAll(dst) // what a clone that failed partway left
	out, err := exec.CommandContext(ctx, "cp", "-pR", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
