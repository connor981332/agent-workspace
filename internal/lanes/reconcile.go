package lanes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/connor981332/agent-workspace/internal/state"
)

// Reconcile brings a lane made under an older configuration up to the current one, where that can
// be done at any time: it links the secret files and copies the local files the configuration now
// declares (plans/agent-feedback.md, item 4). It returns what it added, and, as a note, what it
// cannot add, such as a project the lane has no worktree of. It never replaces a file.
func (m *Manager) Reconcile(ctx context.Context, l state.Lane) (added, notes []string, err error) {
	repos, err := state.LaneRepos(m.Store.DB(), l.Number)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]bool{}
	for _, r := range repos {
		have[r.Project] = true
		pr := m.Cfg.Projects[r.Project]
		if r.Project == "" || pr == nil || pr.Lanes == "shared" {
			continue
		}
		p := repoPlan{project: r.Project, owner: filepath.Join(m.WS.Owner, pr.Directory), path: r.Path, secrets: pr.Secrets, localFiles: pr.LocalFiles}
		missing := func(files []string) []string {
			var out []string
			for _, f := range files {
				if _, err := os.Lstat(filepath.Join(p.owner, f)); err != nil {
					continue // the owner has none to give
				}
				if _, err := os.Lstat(filepath.Join(p.path, f)); err != nil {
					out = append(out, f)
				}
			}
			return out
		}
		secrets, locals := missing(p.secrets), missing(p.localFiles)
		if err := linkSecrets(ctx, p); err != nil {
			return added, notes, err
		}
		if err := copyLocalFiles(ctx, p); err != nil {
			return added, notes, err
		}
		for _, f := range secrets {
			added = append(added, fmt.Sprintf("%s: linked the secret file %s", r.Project, f))
		}
		for _, f := range locals {
			added = append(added, fmt.Sprintf("%s: copied the local file %s", r.Project, f))
		}
	}
	for _, name := range sortedKeys(m.Cfg.Projects) {
		if pr := m.Cfg.Projects[name]; !have[name] && pr.Directory != "." && pr.Lanes != "shared" && !slices.Contains(notes, name) {
			notes = append(notes, fmt.Sprintf("project %s was added after this lane was made, so the lane has no worktree of it → finish or discard the lane, and attach a new one to work on it", name))
		}
	}
	return added, notes, nil
}
