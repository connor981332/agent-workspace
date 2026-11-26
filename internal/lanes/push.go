package lanes

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
)

// PushResult is what aw push did in one of a lane's repositories.
type PushResult struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Remote  string `json:"remote,omitempty"` // the branch's name on origin
	Outcome string `json:"outcome"`          // pushed, nothing to push, failed
	Detail  string `json:"detail,omitempty"`
}

// Failed reports whether the push failed.
func (r PushResult) Failed() bool { return r.Outcome == "failed" }

// LeaseLast is the lease aw push --force-with-lease takes by default: the tip of the remote branch
// that this repository last fetched or pushed, its remote-tracking branch.
const LeaseLast = "last"

// PushOptions are how aw push pushes.
type PushOptions struct {
	// As is the remote name; "" keeps the name a repository was pushed under before, or aw's own.
	As string
	// Lease, when set, lets the push replace the remote branch's history, only if the remote branch
	// is still where this says: LeaseLast, or a commit. "" is a plain push, which only adds commits.
	Lease string
}

// Push pushes a lane's branch to origin, under the team's name for it when that differs from aw's
// (plans/agent-feedback.md, item 9). The lane keeps its own branch, and the upstream is set to the
// remote name, which is recorded so that recap, finish and sweep find the change on the forge.
//
// Without names it pushes every repository with the lane's own commits, and every one pushed before.
// With a lease it may rewrite the remote branch, but never over commits aw has not seen there.
func (m *Manager) Push(ctx context.Context, l state.Lane, opts PushOptions, only []string) ([]PushResult, error) {
	as := opts.As
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	if as != "" {
		if _, err := run.In(m.WS.Owner).Run(ctx, "check-ref-format", "--branch", as); err != nil || strings.HasPrefix(as, "-") {
			return nil, fmt.Errorf("%q is not a valid branch name → choose another with --as", as)
		}
	}
	var known []string
	for _, r := range repos {
		known = append(known, repoKey(r))
	}
	for _, name := range only {
		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("lane %s has no repository %q → name one of %s", Dir(l.Number, m.Cfg.Lanes.Max), name, strings.Join(known, ", "))
		}
	}
	var out []PushResult
	for _, r := range repos {
		key := repoKey(r)
		g := run.In(r.Path)
		if len(only) > 0 && !slices.Contains(only, key) {
			continue
		}
		if len(only) == 0 && r.Published == "" && ownCommits(ctx, g, r) == 0 {
			continue
		}
		name := as
		if name == "" {
			name = r.RemoteBranch()
		}
		res := PushResult{Repo: key, Branch: r.Branch, Remote: name}
		if r.Published != "" && r.Published != name {
			res.Detail = "it was pushed as " + r.Published + " before, which is left on origin"
		}
		if m.Progress != nil {
			m.Progress(fmt.Sprintf("%s: pushing %s to origin as %s", key, r.Branch, name))
		}
		pushArgs := []string{"push", "--quiet", "--set-upstream"}
		if opts.Lease != "" {
			lease, err := leaseFor(ctx, g, name, opts.Lease)
			if err != nil {
				res.Outcome, res.Detail = "failed", err.Error()
				out = append(out, res)
				continue
			}
			pushArgs = append(pushArgs, "--force-with-lease=refs/heads/"+name+":"+lease)
		}
		pushArgs = append(pushArgs, "origin", "refs/heads/"+r.Branch+":refs/heads/"+name)
		if _, err := g.Run(ctx, pushArgs...); err != nil {
			res.Outcome, res.Detail = "failed", err.Error()
			out = append(out, res)
			continue
		}
		res.Outcome = "pushed"
		published := name
		if name == r.Branch {
			published = ""
		}
		if published != r.Published {
			if err := m.Store.Write(ctx, func(tx *sql.Tx) error { return state.SetPublished(tx, l.Number, r.Project, published) }); err != nil {
				return out, fmt.Errorf("%s was pushed as %s, but that could not be recorded (%v) → run aw push again", key, name, err)
			}
		}
		out = append(out, res)
	}
	if len(out) == 0 {
		return []PushResult{{Repo: "", Outcome: "nothing to push", Detail: "no repository in this lane has commits of its own"}}, nil
	}
	return out, nil
}

// leaseFor is the commit the remote branch must still be at for a lease to replace it: the one given,
// or the remote-tracking branch's. A branch new to origin has nothing to lose, so its lease is that
// the branch is still missing there.
func leaseFor(ctx context.Context, g run.Git, name, lease string) (string, error) {
	if lease != LeaseLast {
		sha, err := g.Run(ctx, "rev-parse", "--verify", "--quiet", lease+"^{commit}")
		if err != nil || strings.TrimSpace(sha) == "" {
			return "", fmt.Errorf("--force-with-lease=%s names no commit in this repository → fetch origin %s and give its tip, or use --force-with-lease alone", lease, name)
		}
		return strings.TrimSpace(sha), nil
	}
	tracking := "refs/remotes/origin/" + name
	sha, err := g.Run(ctx, "rev-parse", "--verify", "--quiet", tracking)
	if err != nil || strings.TrimSpace(sha) == "" {
		if remote, lerr := g.Run(ctx, "ls-remote", "--heads", "origin", "refs/heads/"+name); lerr == nil && strings.TrimSpace(remote) == "" {
			return "", nil // not on origin: the lease is that it still is not
		}
		return "", fmt.Errorf("aw has not seen origin/%s here, so it cannot tell what a forced push would replace → git fetch origin %s, look at what is there, and push again", name, name)
	}
	return strings.TrimSpace(sha), nil
}
