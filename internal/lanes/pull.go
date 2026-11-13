package lanes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// PullResult is what aw pull did in one owner checkout.
type PullResult struct {
	Repo    string `json:"repo"` // "." for the workspace repository
	Base    string `json:"base,omitempty"`
	Outcome string `json:"outcome"` // up to date, fast-forwarded, fetched only, failed, skipped
	Detail  string `json:"detail,omitempty"`
}

// Failed reports whether a fetch or a fast-forward failed.
func (r PullResult) Failed() bool { return r.Outcome == "failed" }

// Pull fetches origin in the workspace repository and every project's owner checkout, in parallel,
// and fast-forwards each one that is on origin's default branch. It never merges, rebases or
// switches: a checkout on another branch, or with commits origin lacks, is only fetched. Lanes share
// their repository's refs, so new lanes start from what this fetched.
func (m *Manager) Pull(ctx context.Context) []PullResult {
	type repo struct{ name, dir string }
	var repos []repo
	single := false
	for _, name := range sortedKeys(m.Cfg.Projects) {
		if m.Cfg.Projects[name].Directory == "." {
			single = true
		}
	}
	if !single && workspace.IsGitRepo(m.WS.Owner) {
		repos = append(repos, repo{".", m.WS.Owner})
	}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		repos = append(repos, repo{name, filepath.Join(m.WS.Owner, m.Cfg.Projects[name].Directory)})
	}
	out := make([]PullResult, len(repos))
	type job struct {
		i int
		r repo
	}
	jobs := make([]job, len(repos))
	for i, r := range repos {
		jobs[i] = job{i, r}
	}
	parallel(jobs, func(j job) {
		m.say("%s: fetching origin", j.r.name)
		out[j.i] = pullOne(ctx, j.r.name, j.r.dir)
	})
	return out
}

func pullOne(ctx context.Context, name, dir string) PullResult {
	res := PullResult{Repo: name}
	if _, err := workspace.GitCommonDir(dir); err != nil {
		res.Outcome, res.Detail = "skipped", "not cloned"
		return res
	}
	g := run.In(dir)
	if _, err := g.Line(ctx, "remote", "get-url", "origin"); err != nil {
		res.Outcome, res.Detail = "skipped", "no remote named origin"
		return res
	}
	if _, err := g.Run(ctx, "fetch", "--quiet", "origin"); err != nil {
		res.Outcome, res.Detail = "failed", "git fetch: "+firstLine(err.Error())
		return res
	}
	if !ok(g.OK(ctx, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")) {
		g.Run(ctx, "remote", "set-head", "origin", "--auto") // best effort: main or master is the fallback
	}
	def := defaultBranch(ctx, g)
	if def == "" {
		res.Outcome, res.Detail = "fetched only", "origin has no default branch aw can find"
		return res
	}
	res.Base = shortRef(def)
	current, err := g.Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		res.Outcome, res.Detail = "fetched only", "detached HEAD, left alone"
		return res
	}
	if current != strings.TrimPrefix(def, "refs/remotes/origin/") {
		res.Outcome, res.Detail = "fetched only", "on "+current+", left alone"
		return res
	}
	behind := count(ctx, g, "HEAD.."+def)
	ahead := count(ctx, g, def+"..HEAD")
	switch {
	case behind == 0:
		res.Outcome = "up to date"
		if ahead > 0 {
			res.Detail = fmt.Sprintf("%d local commit%s %s lacks → push %s", ahead, plural(ahead), res.Base, them(ahead))
		}
	case ahead > 0:
		res.Outcome, res.Detail = "fetched only", fmt.Sprintf("%d local commit%s %s lacks, so it cannot fast-forward → rebase or merge, then push", ahead, plural(ahead), res.Base)
	default:
		if _, err := g.Run(ctx, "merge", "--ff-only", "--quiet", def); err != nil {
			res.Outcome, res.Detail = "failed", "fast-forward: "+firstLine(err.Error())+" → commit or stash the changes in the way, then run aw pull again"
			return res
		}
		res.Outcome, res.Detail = "fast-forwarded", fmt.Sprintf("%d commit%s", behind, plural(behind))
	}
	return res
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
