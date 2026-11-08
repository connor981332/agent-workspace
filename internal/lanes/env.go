package lanes

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/connor981332/agent-workspace/internal/state"
)

// Where says where a project command runs: the directory, and the environment contract
// (lifecycle.md, "Environment contract").
type Where struct {
	Dir  string
	Env  []string // KEY=VALUE, added to the caller's environment
	Lane *state.Lane
}

// StateDir is a lane's private state directory, <workspace git dir>/aw/lanes/<NN>, created on use.
func (m *Manager) StateDir(n int) (string, error) {
	dir := filepath.Join(filepath.Dir(m.Store.Path()), "lanes", Dir(n, m.Cfg.Lanes.Max))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

// Locate returns where a project's commands run for the caller: in a lane, its worktree of the
// project (or the owner's checkout of a shared project) with the lane's variables; in the owner
// checkout, the owner's checkout of the project.
func (m *Manager) Locate(project string) (*Where, error) {
	p, ok := m.Cfg.Projects[project]
	if !ok {
		names := sortedKeys(m.Cfg.Projects)
		return nil, fmt.Errorf("there is no project named %q → known: %s", project, strings.Join(names, ", "))
	}
	ownerDir := filepath.Join(m.WS.Owner, p.Directory)
	env := []string{
		"AW_OWNER=" + m.WS.Owner,
		"AW_NAMESPACE=" + m.Cfg.Workspace.Namespace,
		"AW_PROJECT=" + project,
		"AW_TOOL=" + filepath.Join(m.WS.Owner, "bin", "aw"),
	}
	n := m.CallerLane()
	if n == 0 {
		return &Where{Dir: ownerDir, Env: append(env, "AW_PROJECT_PATH="+ownerDir)}, nil
	}
	l, err := state.LaneByNumber(m.Store.DB(), n)
	if err != nil {
		return nil, fmt.Errorf("lane %s has no record in the state → run aw doctor", Dir(n, m.Cfg.Lanes.Max))
	}
	dir := ownerDir
	if p.Lanes != "shared" {
		repos, err := state.LaneRepos(m.Store.DB(), n)
		if err != nil {
			return nil, err
		}
		dir = ""
		for _, r := range repos {
			if r.Project == project || (p.Directory == "." && r.Project != "") {
				dir = r.Path
			}
		}
		if dir == "" {
			return nil, fmt.Errorf("lane %s has no worktree of %s → recreate the lane, or check aw doctor", l.Name, project)
		}
	}
	laneState, err := m.StateDir(n)
	if err != nil {
		return nil, err
	}
	env = append(env,
		"AW_PROJECT_PATH="+dir,
		"AW_LANE="+Dir(n, m.Cfg.Lanes.Max),
		"AW_LANE_NAME="+l.Name,
		"AW_LANE_ROOT="+m.Root(n),
		"AW_LANE_STATE="+laneState,
		"AW_HOSTNAME="+strings.NewReplacer("{lane}", Dir(n, m.Cfg.Lanes.Max), "{namespace}", m.Cfg.Workspace.Namespace).Replace(m.Cfg.Lanes.Hostname),
		"AW_TOOL="+filepath.Join(m.Root(n), "bin", "aw"),
	)
	// The project's env file, written by the prepare hook: plain KEY=VALUE lines.
	if extra, err := readEnvFile(filepath.Join(laneState, project+".env")); err == nil {
		env = append(env, extra...)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &Where{Dir: dir, Env: env, Lane: &l}, nil
}

func readEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, _, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("%s:%d: not a KEY=VALUE line → fix the file the prepare hook writes", path, line)
		}
		out = append(out, text)
	}
	return out, s.Err()
}
