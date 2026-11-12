package lanes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// StaleFetch is how old origin's refs may be before a new lane says so. aw never fetches on its
// own: a fetch costs a network round trip that aw new cannot afford (speed.md).
const StaleFetch = 24 * time.Hour

// defaultBranch is origin's default branch as last fetched, such as refs/remotes/origin/main, or ""
// when the repository has no such ref. origin/HEAD names it after a clone; otherwise main or master.
func defaultBranch(ctx context.Context, g run.Git) string {
	if ref, err := g.Line(ctx, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		if ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")) {
			return ref
		}
	}
	for _, name := range []string{"main", "master"} {
		ref := "refs/remotes/origin/" + name
		if ok(g.OK(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")) {
			return ref
		}
	}
	return ""
}

// shortRef is origin/main for refs/remotes/origin/main.
func shortRef(ref string) string { return strings.TrimPrefix(ref, "refs/remotes/") }

// ownerDrift notes how the owner checkout differs from where a new lane starts: another branch, or
// commits origin's default branch lacks. It records the commits no remote has at all.
func ownerDrift(ctx context.Context, g run.Git, p *repoPlan, def string) {
	name := strings.TrimPrefix(def, "refs/remotes/origin/")
	n := count(ctx, g, "HEAD", "^"+def)
	if n > 0 {
		p.unpushed = count(ctx, g, "HEAD", "--not", "--remotes")
	}
	switch {
	case p.current != name:
		p.notes = append(p.notes, fmt.Sprintf("%s: your checkout is on %s, and the lane starts from %s → to start from %s instead, use --branch %s",
			label(*p), p.current, shortRef(def), p.current, p.current))
	case n > 0:
		p.notes = append(p.notes, fmt.Sprintf("%s: your checkout has %d commit%s that %s lacks, and the lane starts without %s → push %s to include %s in new lanes",
			label(*p), n, plural(n), shortRef(def), them(n), them(n), them(n)))
	}
}

func count(ctx context.Context, g run.Git, args ...string) int {
	out, err := g.Line(ctx, append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// fetchedAt is when origin's refs were last updated in a repository: by a fetch, a pull or a push.
// The zero time means it cannot tell.
func fetchedAt(owner, def string) time.Time {
	common, err := workspace.GitCommonDir(owner)
	if err != nil {
		return time.Time{}
	}
	var last time.Time
	for _, f := range []string{"FETCH_HEAD", filepath.Join("logs", filepath.FromSlash(def))} {
		if info, err := os.Stat(filepath.Join(common, f)); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}

// staleNote says, in one line, which repositories' copies of origin are older than StaleFetch.
func staleNote(plans []repoPlan, now time.Time) string {
	var names []string
	var oldest time.Duration
	for _, p := range plans {
		if p.fetched.IsZero() {
			continue
		}
		if age := now.Sub(p.fetched); age > StaleFetch {
			names = append(names, label(p))
			oldest = max(oldest, age)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("the lane starts from origin as last fetched, up to %s ago (%s) → run aw pull to fetch, and to bring the owner checkouts up to date",
		age(oldest), strings.Join(names, ", "))
}

func age(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		return fmt.Sprintf("%d day%s", days, plural(days))
	}
	h := max(1, int(d.Hours()))
	return fmt.Sprintf("%d hour%s", h, plural(h))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// notes gathers what the person should know about where a new lane starts.
func notes(plans []repoPlan) []string {
	var out []string
	for _, p := range plans {
		out = append(out, p.notes...)
	}
	if s := staleNote(plans, time.Now()); s != "" {
		out = append(out, s)
	}
	return out
}
