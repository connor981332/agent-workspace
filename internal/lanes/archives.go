package lanes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/gitx/run"
)

// ArchivePrefix is where aw discard, aw reset-branch and the orphan cleanup keep the tips they took
// away: refs/aw/archive/<name>/<time> in each repository.
const ArchivePrefix = "refs/aw/archive/"

// Archive is one archived piece of work: what one discard (or reset, or orphan cleanup) kept, across
// the repositories it touched.
type Archive struct {
	ID    string         `json:"id"` // <name>/<time>
	Name  string         `json:"name"`
	Time  string         `json:"time"`
	Repos []ArchivedRepo `json:"repos"`
}

// ArchivedRepo is one repository's archived tip.
type ArchivedRepo struct {
	Repo    string `json:"repo"` // the project, or "workspace repository"
	Ref     string `json:"ref"`
	Head    string `json:"head"`
	Subject string `json:"subject"`
}

// archiveRepos are the workspace's repositories, by label and owner directory.
func (m *Manager) archiveRepos() [][2]string {
	out := [][2]string{{"workspace repository", m.WS.Owner}}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		p := m.Cfg.Projects[name]
		if filepath.Clean(p.Directory) == "." {
			out[0][0] = name
			continue
		}
		if _, err := os.Stat(filepath.Join(m.WS.Owner, p.Directory, ".git")); err == nil {
			out = append(out, [2]string{name, filepath.Join(m.WS.Owner, p.Directory)})
		}
	}
	return out
}

// Archives lists what is archived, newest first. It only reads.
func (m *Manager) Archives(ctx context.Context) ([]Archive, error) {
	byID := map[string]*Archive{}
	for _, r := range m.archiveRepos() {
		out, err := run.In(r[1]).Run(ctx, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(contents:subject)", ArchivePrefix)
		if err != nil {
			continue // a repository that cannot be read has nothing to list
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			f := strings.SplitN(line, "\x00", 3)
			if len(f) != 3 {
				continue
			}
			id := strings.TrimPrefix(f[0], ArchivePrefix)
			name, stamp, ok := cutLast(id, "/")
			if !ok {
				continue
			}
			a := byID[id]
			if a == nil {
				a = &Archive{ID: id, Name: name, Time: stamp}
				byID[id] = a
			}
			a.Repos = append(a.Repos, ArchivedRepo{Repo: r[0], Ref: f[0], Head: f[1], Subject: f[2]})
		}
	}
	out := make([]Archive, 0, len(byID))
	for _, a := range byID {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b Archive) int {
		if c := strings.Compare(b.Time, a.Time); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

// RestoreArchive makes a branch at an archive's tip in each repository it holds, so that a lane can
// start from it with --branch. ref is <name>, meaning its newest archive, or <name>/<time>. Nothing
// is changed if the branch already exists in any of them.
func (m *Manager) RestoreArchive(ctx context.Context, ref, branch string) (Archive, error) {
	all, err := m.Archives(ctx)
	if err != nil {
		return Archive{}, err
	}
	var found *Archive
	for i := range all {
		if all[i].ID == ref || (found == nil && all[i].Name == ref) {
			found = &all[i]
		}
	}
	if found == nil {
		return Archive{}, fmt.Errorf("nothing is archived as %s → aw archives lists what is", ref)
	}
	if branch == "" {
		branch = "restored/" + found.Name
	}
	if _, err := run.In(m.WS.Owner).Run(ctx, "check-ref-format", "--branch", branch); err != nil || strings.HasPrefix(branch, "-") {
		return Archive{}, fmt.Errorf("%q is not a branch name Git accepts → pass another with --branch", branch)
	}
	dirs := map[string]string{}
	for _, r := range m.archiveRepos() {
		dirs[r[0]] = r[1]
	}
	var taken []string
	for _, r := range found.Repos {
		if ok, _ := run.In(dirs[r.Repo]).OK(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); ok {
			taken = append(taken, r.Repo)
		}
	}
	if len(taken) > 0 {
		return Archive{}, fmt.Errorf("%s already has a branch %s; nothing was changed → pass another name with --branch", strings.Join(taken, ", "), branch)
	}
	for _, r := range found.Repos {
		if _, err := run.In(dirs[r.Repo]).Run(ctx, "branch", branch, r.Head); err != nil {
			return Archive{}, errors.Join(fmt.Errorf("restoring %s in %s", found.ID, r.Repo), err)
		}
	}
	return *found, nil
}
