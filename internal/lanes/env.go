package lanes

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
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
	return m.LocateIn(m.CallerLane(), project)
}

// LocateIn returns where a project's commands run in lane n, or in the owner checkout for lane 0.
// The project "" is the lane root, for the steps that belong to no one project.
func (m *Manager) LocateIn(n int, project string) (*Where, error) {
	var p *config.Project
	ownerDir := m.WS.Owner
	if project != "" {
		var ok bool
		if p, ok = m.Cfg.Projects[project]; !ok {
			names := sortedKeys(m.Cfg.Projects)
			for _, n := range names {
				if strings.HasSuffix(n, "-"+project) {
					return nil, fmt.Errorf("there is no project named %q → did you mean %s? Known: %s", project, n, strings.Join(names, ", "))
				}
			}
			return nil, fmt.Errorf("there is no project named %q → known: %s", project, strings.Join(names, ", "))
		}
		ownerDir = filepath.Join(m.WS.Owner, p.Directory)
	}
	env := []string{
		"AW_OWNER=" + m.WS.Owner,
		"AW_NAMESPACE=" + m.Cfg.Workspace.Namespace,
		"AW_TOOL=" + filepath.Join(m.WS.Owner, "bin", "aw"),
	}
	// Each team option's choice in effect, for hooks and commands: AW_OPTION_EVENT_BUS=real.
	for _, name := range sortedKeys(m.Cfg.Chosen) {
		env = append(env, "AW_OPTION_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_"))+"="+m.Cfg.Chosen[name])
	}
	if workspace.IsRehearsal(m.WS.Owner) {
		env = append(env, "AW_REHEARSAL=1", "AW_INFRA_PROJECT="+m.Cfg.Workspace.Namespace+"-infra")
		if p != nil && p.Runner != "devcontainer" {
			env = append(env, hostComposeEnv(ownerDir, m.Cfg.Workspace.Namespace, project, n, m.Cfg.Ports.Offset)...)
		}
	}
	if project != "" {
		env = append(env, "AW_PROJECT="+project)
		for _, name := range sortedKeys(p.Env) {
			env = append(env, name+"="+m.expandLaneEnv(p.Env[name], n))
		}
	}
	for _, group := range sortedKeys(m.Cfg.LaneEnv) {
		for _, name := range sortedKeys(m.Cfg.LaneEnv[group]) {
			env = append(env, name+"="+m.expandLaneEnv(m.Cfg.LaneEnv[group][name], n))
		}
	}
	if n == 0 {
		return &Where{Dir: ownerDir, Env: append(env, "AW_PROJECT_PATH="+ownerDir)}, nil
	}
	l, err := state.LaneByNumber(m.Store.DB(), n)
	if err != nil {
		return nil, fmt.Errorf("lane %s has no record in the state → run aw doctor", Dir(n, m.Cfg.Lanes.Max))
	}
	dir := m.Root(n)
	if p != nil && p.Lanes == "shared" {
		dir = ownerDir
	} else if p != nil {
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
	if project != "" {
		env = append(env, "AW_PROJECT_PATH="+dir)
	}
	env = append(env,
		"AW_LANE="+Dir(n, m.Cfg.Lanes.Max),
		"AW_LANE_NAME="+l.Name,
		"AW_LANE_ROOT="+m.Root(n),
		"AW_LANE_STATE="+laneState,
		"AW_HOSTNAME="+strings.NewReplacer("{lane}", Dir(n, m.Cfg.Lanes.Max), "{namespace}", m.Cfg.Workspace.Namespace).Replace(m.Cfg.Lanes.Hostname),
		"AW_TOOL="+filepath.Join(m.Root(n), "bin", "aw"),
	)
	// What the providers recorded for the lane's commands.
	if entries, err := os.ReadDir(filepath.Join(laneState, "data")); err == nil {
		for _, e := range entries { // sorted by name
			extra, err := readEnvFile(filepath.Join(laneState, "data", e.Name()))
			if err != nil {
				return nil, err
			}
			env = append(env, extra...)
		}
	}
	// The project's env file, written by the prepare hook: plain KEY=VALUE lines.
	if project != "" {
		if extra, err := readEnvFile(filepath.Join(laneState, project+".env")); err == nil {
			env = append(env, extra...)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
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

var portTemplate = regexp.MustCompile(`\{port:(\d+)\}`)

// expandLaneEnv fills in a lane_env value. The owner's checkout is lane "owner".
func (m *Manager) expandLaneEnv(value string, n int) string {
	lane := "owner"
	if n > 0 {
		lane = Dir(n, m.Cfg.Lanes.Max)
	}
	value = strings.NewReplacer("{lane}", lane, "{namespace}", m.Cfg.Workspace.Namespace).Replace(value)
	return portTemplate.ReplaceAllStringFunc(value, func(match string) string {
		p, _ := strconv.Atoi(portTemplate.FindStringSubmatch(match)[1])
		return strconv.Itoa(p + m.Cfg.Ports.Offset)
	})
}
