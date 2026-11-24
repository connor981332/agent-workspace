// Package evidence decides whether a lane's work is proven merged (lifecycle.md, "Merge proof"): a
// repository's head is an ancestor of its base, or its commits are patch-equivalent to the base's
// in a history without merges, or merging it into the base would change nothing, or a forge
// reports exactly one merged change at exactly this head. Anything else, including anything that
// cannot be read, is not proof.
package evidence

import (
	"context"
	"errors"
	"fmt"
	"github.com/connor981332/agent-workspace/internal/workspace"
	"path"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
)

// Proof kinds.
const (
	Ancestor = "ancestor"         // the head is in the base
	Patch    = "patch-equivalent" // every commit's patch is in the base
	Content  = "content"          // merging the head into the base would change nothing
	Forge    = "forge"            // the forge reports this exact head merged
	// Gone is not a proof of a merge: the worktree and the branch were both already removed, by an
	// earlier removal of the lane that did not finish, so nothing of the lane is left to lose.
	Gone = "gone"
)

// Repo is what is known about one repository of a lane.
type Repo struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Head    string `json:"head"`
	BaseRef string `json:"base_ref"`
	Base    string `json:"base"`  // the base ref's commit now
	Clean   bool   `json:"clean"` // no tracked changes, no untracked files, no blocking ignored files
	Dirty   string `json:"dirty"` // why not clean
	// Ignored are the ignored files and directories that keep the lane, when those are what makes it
	// not clean.
	Ignored []string `json:"ignored,omitempty"`
	Proof   string   `json:"proof"`   // Ancestor, Patch, Forge, or ""
	Reason  string   `json:"reason"`  // why there is no proof
	Unknown bool     `json:"unknown"` // something could not be read: protect
	// Forge is what the forge said, when it was asked.
	Forge any `json:"forge,omitempty"`
	// LastFetch is when a remote-tracking base was last updated (RFC 3339), when the repository is
	// not proven merged and that can be read: the proof is only as fresh as it.
	LastFetch string `json:"last_fetch,omitempty"`
}

// Options are what the caller knows about the lane.
type Options struct {
	// Allowed are ignored paths that never block: generated paths (pathspecs) and the links aw made.
	Generated []string
	Ours      []string
	// ExpectBranch is the branch the worktree must be on.
	ExpectBranch string
	// DetachedAt, when set, is the one commit a detached HEAD may be at: a repository that an
	// earlier recycle of the lane had already reset to its base before another one failed.
	DetachedAt string
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
		head, herr := g.Line(ctx, "rev-parse", "--verify", "HEAD^{commit}")
		if o.DetachedAt == "" || herr != nil || head != o.DetachedAt {
			return unknown("the worktree is on a detached HEAD, not on its branch")
		}
		branch = "" // already reset by an earlier recycle; its proof and cleanliness still decide
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
	status = workspace.WithoutMaterialized(g.Dir, status)
	if status != "" {
		r.Dirty = summarize("changed or untracked", status)
	} else {
		listed, err := g.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
		if err != nil {
			return unknown("listing ignored files failed: %v", err)
		}
		var kept []string
		for _, e := range strings.Split(strings.TrimRight(listed, "\n"), "\n") {
			if e == "" || Declared(e, o.Generated, o.Ours) {
				continue
			}
			// Git lists a wholly ignored directory as one entry. When a declared path lies inside
			// it, what else it holds decides, so it is listed file by file.
			if strings.HasSuffix(e, "/") && declaresInside(e, o.Generated, o.Ours) {
				inside, err := g.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--", e)
				if err != nil {
					return unknown("listing ignored files failed: %v", err)
				}
				for _, f := range strings.Split(strings.TrimRight(inside, "\n"), "\n") {
					if f != "" && !Declared(f, o.Generated, o.Ours) {
						kept = append(kept, f)
					}
				}
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) > 0 {
			slices.Sort(kept) // listing a directory's files can name one twice
			kept = slices.Compact(kept)
			r.Ignored = kept
			r.Dirty = summarize("ignored files that are not declared generated", strings.Join(kept, "\n"))
		}
	}
	r.Clean = r.Dirty == ""
	return prove(ctx, g, r, baseRef)
}

// CheckRemoved examines a repository whose worktree an earlier removal of the lane already took
// away, so that a retry can finish what it started (#113). With no worktree there is no uncommitted
// work: what is left to protect is the branch, in the owner checkout at owner, and it is proven or
// not as Check proves a worktree's head. With the branch gone too, nothing is left.
func CheckRemoved(ctx context.Context, owner, path, branch, baseRef string) Repo {
	r := Repo{Path: path, Branch: branch, BaseRef: baseRef, Clean: true}
	g := run.In(owner)
	if branch == "" {
		r.Proof = Gone
		return r
	}
	head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		r.Proof = Gone
		return r
	}
	r.Head = head
	if r.Base, err = g.Line(ctx, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		r.Unknown, r.Reason = true, fmt.Sprintf("its base %s cannot be read", baseRef)
		return r
	}
	return prove(ctx, g, r, baseRef)
}

// prove works out whether r.Head is merged into r.Base, in the repository g.
func prove(ctx context.Context, g run.Git, r Repo, baseRef string) Repo {
	unknown := func(format string, args ...any) Repo {
		r.Unknown, r.Reason = true, fmt.Sprintf(format, args...)
		return r
	}
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
		r.Reason = "not proven merged: its head is not in " + baseRef + ", its history has merge commits, which patch equivalence cannot prove"
	} else {
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
		r.Reason = fmt.Sprintf("not proven merged: %d commit%s not in %s: its head is not there, no commit there has the same patch", unmerged, plural(unmerged), baseRef)
	}
	// A squash or rebase merge leaves neither the commits nor their patches in the base, but the
	// base then holds every change the lane made: merging the lane in would change nothing. This
	// proves that nothing would be lost, not who merged it, which is what recycling needs. A
	// conflict, or any difference, leaves the lane unproven.
	switch ok, why := sameAsBase(ctx, g, r.Base, r.Head); {
	case ok:
		r.Proof, r.Reason = Content, ""
	case why != "":
		r.Reason += ", and " + why
	}
	return r
}

// sameAsBase reports whether merging head into base would leave base's tree unchanged, and if not,
// why, in words for a reason.
func sameAsBase(ctx context.Context, g run.Git, base, head string) (bool, string) {
	out, err := g.Run(ctx, "merge-tree", "--write-tree", "--no-messages", base, head)
	merged, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	var ge *run.Error
	switch {
	case errors.As(err, &ge) && ge.ExitCode() == 1:
		return false, "merging it would conflict with the base"
	case errors.As(err, &ge) && (strings.Contains(ge.Stderr, "usage:") || strings.Contains(ge.Stderr, "unknown option")):
		return false, "this Git cannot compare content (Git 2.38 or later can)"
	case err != nil:
		return false, "comparing its content with the base failed"
	}
	tree, err := g.Line(ctx, "rev-parse", "--verify", base+"^{tree}")
	if err != nil {
		return false, ""
	}
	if merged == tree {
		return true, ""
	}
	changed, err := g.Run(ctx, "diff-tree", "-r", "--name-only", tree, merged)
	if err != nil {
		return false, "merging it would change the base"
	}
	n := len(strings.Fields(changed))
	return false, fmt.Sprintf("merging it would change %d file%s", n, plural(n))
}

// declaresInside reports whether a declared path could lie inside dir ("dir/"): one under it, or a
// pattern without a slash, which matches at any depth.
func declaresInside(dir string, lists ...[]string) bool {
	for _, list := range lists {
		for _, p := range list {
			p = strings.TrimPrefix(p, "/")
			if strings.HasPrefix(p, dir) || (strings.ContainsAny(p, "*?[") && !strings.Contains(strings.TrimSuffix(p, "/"), "/")) {
				return true
			}
		}
	}
	return false
}

// allowed reports whether an ignored entry (a file, or a directory as "dir/") is a declared
// generated path, one of aw's own links, or under one of them. A directory Git collapsed above a
// declared path is not allowed here: Check lists what it holds instead.
// Declared reports whether an ignored entry (a path, or a directory ending in /) is covered by one
// of the lists: the path itself or a directory above it, or a glob. As in .gitignore, ** spans any
// number of directories wherever it is, a pattern without a slash matches a name at any depth unless
// it starts with one, and
// a pattern that matches a directory covers everything in it. aw sweep, aw inspect, aw rehearse and
// aw doctor --projects (accepted_findings) all use it, so a path one counts as declared, all do.
func Declared(entry string, lists ...[]string) bool {
	e := strings.Trim(entry, "/")
	segs := strings.Split(e, "/")
	for _, list := range lists {
		for _, p := range list {
			anchored := strings.HasPrefix(p, "/")
			p = strings.Trim(p, "/")
			if p == "" {
				continue
			}
			if !anchored && !strings.Contains(p, "/") {
				p = "**/" + p // as in .gitignore: a pattern without a slash matches at any depth
			}
			ps := strings.Split(p, "/")
			// A pattern that matches a directory above the entry covers it too.
			for i := len(segs); i >= 1; i-- {
				if segmentsMatch(ps, segs[:i]) {
					return true
				}
			}
		}
	}
	return false
}

// segmentsMatch matches path segments against pattern segments, where ** spans any number of
// directories, none included.
func segmentsMatch(pattern, p []string) bool {
	if len(pattern) == 0 {
		return len(p) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(p); i++ {
			if segmentsMatch(pattern[1:], p[i:]) {
				return true
			}
		}
		return false
	}
	if len(p) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], p[0])
	return ok && err == nil && segmentsMatch(pattern[1:], p[1:])
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
