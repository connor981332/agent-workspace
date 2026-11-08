// Package evidence decides whether a lane's work is proven merged (lifecycle.md, "Merge proof"): a
// repository's head is an ancestor of its base, or its commits are patch-equivalent to the base's
// in a history without merges, or a forge reports exactly one merged change at exactly this head.
// Anything else, including anything that cannot be read, is not proof.
package evidence

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
)

// Proof kinds.
const (
	Ancestor = "ancestor"         // the head is in the base
	Patch    = "patch-equivalent" // every commit's patch is in the base
	Forge    = "forge"            // the forge reports this exact head merged
)

// Repo is what is known about one repository of a lane.
type Repo struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Head    string `json:"head"`
	BaseRef string `json:"base_ref"`
	Base    string `json:"base"`    // the base ref's commit now
	Clean   bool   `json:"clean"`   // no tracked changes, no untracked files, no blocking ignored files
	Dirty   string `json:"dirty"`   // why not clean
	Proof   string `json:"proof"`   // Ancestor, Patch, Forge, or ""
	Reason  string `json:"reason"`  // why there is no proof
	Unknown bool   `json:"unknown"` // something could not be read: protect
}

// Options are what the caller knows about the lane.
type Options struct {
	// Allowed are ignored paths that never block: generated paths (pathspecs) and the links aw made.
	Generated []string
	Ours      []string
	// ExpectBranch is the branch the worktree must be on.
	ExpectBranch string
}

// Check examines one worktree.
func Check(ctx context.Context, path, baseRef string, o Options) Repo {
	r := Repo{Path: path, BaseRef: baseRef}
	g := run.In(path)
	unknown := func(format string, args ...any) Repo {
		r.Unknown, r.Reason = true, fmt.Sprintf(format, args...)
		return r
	}

	branch, err := g.Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return unknown("the worktree is on a detached HEAD, not on its branch")
	}
	r.Branch = branch
	if o.ExpectBranch != "" && branch != o.ExpectBranch {
		return unknown("the worktree is on %s, not on the lane's branch %s", branch, o.ExpectBranch)
	}
	if r.Head, err = g.Line(ctx, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return unknown("its head cannot be read")
	}
	if r.Base, err = g.Line(ctx, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		return unknown("its base %s cannot be read", baseRef)
	}

	// Clean: no tracked changes, no untracked files, and no ignored files except generated ones and
	// the links aw made. Ignored files may be someone's work.
	status, err := g.Run(ctx, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return unknown("git status failed: %v", err)
	}
	if status != "" {
		r.Dirty = summarize("changed or untracked", status)
	} else {
		listed, err := g.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
		if err != nil {
			return unknown("listing ignored files failed: %v", err)
		}
		var kept []string
		for _, e := range strings.Split(strings.TrimRight(listed, "\n"), "\n") {
			if e != "" && !allowed(e, o.Generated, o.Ours) {
				kept = append(kept, e)
			}
		}
		ignored := strings.Join(kept, "\n")
		if ignored != "" {
			r.Dirty = summarize("ignored files that are not declared generated", ignored)
		}
	}
	r.Clean = r.Dirty == ""

	switch ok, err := g.OK(ctx, "merge-base", "--is-ancestor", r.Head, r.Base); {
	case err != nil:
		return unknown("checking ancestry failed: %v", err)
	case ok:
		r.Proof = Ancestor
		return r
	}
	merges, err := g.Line(ctx, "rev-list", "--merges", r.Base+".."+r.Head)
	if err != nil {
		return unknown("listing the lane's commits failed: %v", err)
	}
	if merges != "" {
		r.Reason = "not in " + baseRef + ", and its history has merge commits, which patch equivalence cannot prove"
		return r
	}
	cherry, err := g.Run(ctx, "cherry", r.Base, r.Head)
	if err != nil {
		return unknown("comparing patches failed: %v", err)
	}
	var unmerged int
	for _, line := range strings.Split(strings.TrimSpace(cherry), "\n") {
		if strings.HasPrefix(line, "+") {
			unmerged++
		}
	}
	if unmerged == 0 {
		r.Proof = Patch
		return r
	}
	r.Reason = fmt.Sprintf("not proven merged: %d commit%s not in %s", unmerged, plural(unmerged), baseRef)
	return r
}

// allowed reports whether an ignored entry (a file, or a directory as "dir/") is a declared
// generated path, one of aw's own links, or under one of them. A directory Git collapsed above a
// declared path is not allowed: what else it holds is unknown.
func allowed(entry string, lists ...[]string) bool {
	e := strings.TrimSuffix(entry, "/")
	for _, list := range lists {
		for _, p := range list {
			p = strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
			if e == p || strings.HasPrefix(e, p+"/") {
				return true
			}
			if strings.ContainsAny(p, "*?[") {
				name := e
				if !strings.Contains(p, "/") {
					name = path.Base(e) // as in .gitignore: a pattern without a slash matches at any depth
				}
				if ok, _ := path.Match(p, name); ok {
					return true
				}
			}
		}
	}
	return false
}

func summarize(what, out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	shown := lines
	if len(shown) > 3 {
		shown = shown[:3]
	}
	for i, l := range shown {
		shown[i] = strings.TrimSpace(l)
	}
	more := ""
	if len(lines) > 3 {
		more = fmt.Sprintf(" and %d more", len(lines)-3)
	}
	return fmt.Sprintf("%d %s (%s%s)", len(lines), what, strings.Join(shown, ", "), more)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
