package lanes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// BackupPrefix is where aw backup keeps a lane's saved tips: refs/aw/backup/<lane>/<label> in each
// repository. They are local: aw push pushes only branches.
const BackupPrefix = "refs/aw/backup/"

// Backup is one saved tip of a lane's branch in one repository.
type Backup struct {
	Label   string `json:"label"`
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	Head    string `json:"head"`
	Subject string `json:"subject"`
}

func backupRef(lane, label string) string { return BackupPrefix + lane + "/" + label }

// checkLabel refuses a label Git would not take as one ref component.
func (m *Manager) checkLabel(ctx context.Context, lane, label string) error {
	if label == "" || strings.Contains(label, "/") || strings.HasPrefix(label, "-") {
		return fmt.Errorf("%q is not a backup label → use letters, digits, dots, dashes and underscores, with no slash", label)
	}
	if _, err := run.In(m.WS.Owner).Run(ctx, "check-ref-format", backupRef(lane, label)); err != nil {
		return fmt.Errorf("%q is not a backup label Git accepts → choose another", label)
	}
	return nil
}

// branchRepos are the repositories of a lane that have a branch of its own.
func branchRepos(repos []state.LaneRepo) []state.LaneRepo {
	var out []state.LaneRepo
	for _, r := range repos {
		if r.Branch != "" {
			out = append(out, r)
		}
	}
	return out
}

// SaveBackup saves the tip of the lane's branch in every repository it has, under label (a time
// when ""), before a rebase, a squash or a rebuild. A label in use is never overwritten.
func (m *Manager) SaveBackup(ctx context.Context, l state.Lane, label string, now time.Time) ([]Backup, error) {
	if label == "" {
		label = now.UTC().Format(archiveStamp)
	}
	if err := m.checkLabel(ctx, l.Name, label); err != nil {
		return nil, err
	}
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	repos = branchRepos(repos)
	type tip struct {
		r    state.LaneRepo
		g    run.Git
		head string
	}
	var tips []tip
	for _, r := range repos {
		g := run.In(filepath.Join(m.WS.Owner, m.relOf(r)))
		if ok, _ := g.OK(ctx, "rev-parse", "--verify", "--quiet", backupRef(l.Name, label)); ok {
			return nil, fmt.Errorf("lane %s already has a backup %s in %s; nothing was saved → choose another label", l.Name, label, repoLabel(r))
		}
		head, err := g.Line(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch)
		if err != nil {
			continue // not made yet: nothing to save
		}
		tips = append(tips, tip{r, g, head})
	}
	if len(tips) == 0 {
		return nil, fmt.Errorf("lane %s has no branch to back up yet", l.Name)
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return nil, err
	}
	var out []Backup
	for _, t := range tips {
		ref := backupRef(l.Name, label)
		if _, err := t.g.Run(ctx, "update-ref", ref, t.head, ""); err != nil {
			return out, fmt.Errorf("saving %s: %w", repoLabel(t.r), err)
		}
		subject, _ := t.g.Line(ctx, "log", "-1", "--format=%s", t.head)
		out = append(out, Backup{Label: label, Repo: repoLabel(t.r), Ref: ref, Head: t.head, Subject: subject})
	}
	return out, nil
}

// Backups lists a lane's backups, by label, then repository. It only reads.
func (m *Manager) Backups(ctx context.Context, l state.Lane) ([]Backup, error) {
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, r := range branchRepos(repos) {
		g := run.In(filepath.Join(m.WS.Owner, m.relOf(r)))
		list, err := g.Run(ctx, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(contents:subject)", BackupPrefix+l.Name+"/")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(list, "\n"), "\n") {
			f := strings.SplitN(line, "\x00", 3)
			if len(f) != 3 {
				continue
			}
			out = append(out, Backup{Label: strings.TrimPrefix(f[0], BackupPrefix+l.Name+"/"), Repo: repoLabel(r), Ref: f[0], Head: f[1], Subject: f[2]})
		}
	}
	slices.SortStableFunc(out, func(a, b Backup) int { return strings.Compare(a.Label, b.Label) })
	return out, nil
}

// RestoreBackup moves the lane's branch back to a backup, in every repository that has it. The work
// it moves away from is saved first, as a backup of its own, so restoring loses nothing; uncommitted
// work is refused, since a reset would lose it.
func (m *Manager) RestoreBackup(ctx context.Context, l state.Lane, label string, now time.Time) (saved []Backup, restored []Backup, err error) {
	if err := m.checkLabel(ctx, l.Name, label); err != nil {
		return nil, nil, err
	}
	all, err := m.Backups(ctx, l)
	if err != nil {
		return nil, nil, err
	}
	var want []Backup
	for _, b := range all {
		if b.Label == label {
			want = append(want, b)
		}
	}
	if len(want) == 0 {
		return nil, nil, fmt.Errorf("lane %s has no backup %s → aw backup --list lists them", l.Name, label)
	}
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, nil, err
	}
	byLabel := map[string]state.LaneRepo{}
	for _, r := range repos {
		byLabel[repoLabel(r)] = r
	}
	var dirty []string
	for _, b := range want {
		r := byLabel[b.Repo]
		if _, err := os.Stat(r.Path); err != nil {
			return nil, nil, fmt.Errorf("%s has no worktree in lane %s; nothing was changed → aw use %s, then restore again", b.Repo, l.Name, b.Repo)
		}
		status, err := run.In(r.Path).Run(ctx, "status", "--porcelain=v1", "-uall")
		if err != nil {
			return nil, nil, err
		}
		if workspace.WithoutMaterialized(r.Path, status) != "" {
			dirty = append(dirty, b.Repo)
		}
		if branch, _ := run.In(r.Path).Line(ctx, "symbolic-ref", "--quiet", "--short", "HEAD"); branch != r.Branch {
			return nil, nil, fmt.Errorf("%s is not on the lane's branch, %s; nothing was changed → git switch %s, then restore again", b.Repo, r.Branch, r.Branch)
		}
	}
	if len(dirty) > 0 {
		return nil, nil, fmt.Errorf("lane %s has uncommitted work in %s, which a restore would lose; nothing was changed → commit it (aw backup keeps it), then restore again", l.Name, strings.Join(dirty, ", "))
	}
	// What the restore moves away from, unless it is already the backup.
	moved := false
	for _, b := range want {
		r := byLabel[b.Repo]
		if head, _ := run.In(r.Path).Line(ctx, "rev-parse", "HEAD"); head != b.Head {
			moved = true
		}
	}
	if moved {
		before := "before-" + label + "-" + now.UTC().Format(archiveStamp)
		if saved, err = m.SaveBackup(ctx, l, before, now); err != nil {
			return nil, nil, err
		}
	}
	if err := m.authorizeRefs(ctx); err != nil {
		return saved, nil, err
	}
	for _, b := range want {
		r := byLabel[b.Repo]
		if _, err := run.In(r.Path).Run(ctx, "reset", "--quiet", "--hard", b.Head); err != nil {
			return saved, restored, fmt.Errorf("restoring %s: %w; the work before it is saved as backup %s", b.Repo, err, saved[0].Label)
		}
		restored = append(restored, b)
	}
	return saved, restored, nil
}

// releaseBackups, when a lane is removed, drops each backup whose commit the base or the lane's
// final tip already holds, and archives the rest under refs/aw/archive/<lane>-backup-<label>, so
// that aw archives lists them and no work is lost.
func releaseBackups(ctx context.Context, g run.Git, lane, base, tip string, now time.Time) error {
	list, err := g.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)", BackupPrefix+lane+"/")
	if err != nil || strings.TrimSpace(list) == "" {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(list), "\n") {
		ref, sha, cut := strings.Cut(line, " ")
		if !cut {
			continue
		}
		held := false
		for _, into := range []string{base, tip} {
			if into != "" && ok(g.OK(ctx, "merge-base", "--is-ancestor", sha, into)) {
				held = true
			}
		}
		if !held {
			if err := archiveTip(ctx, g, lane+"-backup-"+strings.TrimPrefix(ref, BackupPrefix+lane+"/"), sha, now); err != nil {
				return err
			}
		}
		if _, err := g.Run(ctx, "update-ref", "-d", ref, sha); err != nil {
			return err
		}
	}
	return nil
}
