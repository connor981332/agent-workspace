// Package doctor checks a workspace and the machine, read-only (R14). Every check that fails or
// warns names its fix. It works before setup has run, and it writes nothing.
package doctor

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/launcher"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/runners"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/timing"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Status of a check.
const (
	OK   = "ok"
	Info = "info"
	Warn = "warn"
	Fail = "fail"
)

// Check is one result.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Report is every check, in order.
type Report struct {
	OK      bool           `json:"ok"`
	Owner   string         `json:"owner,omitempty"`
	Checks  []Check        `json:"checks"`
	Timings []timing.Phase `json:"timings,omitempty"`
}

// MinGit is the oldest Git aw supports (plan §9, confirmed by the ref-hook spike).
var MinGit = [3]int{2, 34, 0}

// Run checks the workspace that contains dir. version is the running binary's.
func Run(dir, version string, withTimings bool) *Report {
	r := &Report{}
	add := func(c Check) { r.Checks = append(r.Checks, c) }
	defer func() {
		r.OK = true
		for _, c := range r.Checks {
			if c.Status == Fail {
				r.OK = false
			}
		}
	}()

	t := timing.Start()
	ws, err := workspace.Find(dir)
	t.Mark("find workspace")
	if err != nil {
		msg, fix, _ := strings.Cut(err.Error(), " → ")
		add(Check{Name: "workspace", Status: Fail, Message: msg, Fix: fix})
		addMachine(add)
		return r
	}
	r.Owner = ws.Owner
	where := "the owner checkout"
	if ws.LaneRoot != "" {
		where = "lane root " + ws.LaneRoot
	}
	add(Check{Name: "workspace", Status: OK, Message: fmt.Sprintf("%s (running in %s)", ws.Owner, where)})

	stateDir, err := ws.StateDir()
	if err != nil {
		add(Check{Name: "git repository", Status: Fail, Message: firstPart(err), Fix: fixPart(err, "clone the workspace repository again")})
	} else {
		add(Check{Name: "git repository", Status: OK, Message: filepath.Dir(stateDir)})
	}

	c, err := config.Load(ws.Owner)
	t.Mark("load configuration")
	var cerrs config.Errors
	switch {
	case errors.As(err, &cerrs):
		for _, e := range cerrs {
			add(Check{Name: "configuration", Status: Fail, Message: e.Pos.String() + ": " + keyed(e), Fix: e.Fix})
		}
	case err != nil:
		add(Check{Name: "configuration", Status: Fail, Message: firstPart(err), Fix: fixPart(err, "check workspace.toml")})
	default:
		add(Check{Name: "configuration", Status: OK, Message: fmt.Sprintf("valid (%d projects, schema %d)", len(c.Projects), c.Workspace.Schema)})
		for _, ref := range c.Refused {
			add(Check{Name: "configuration", Status: Warn,
				Message: fmt.Sprintf("%s: %s is locked by the team (lock %q), so this setting is ignored", ref.Pos, ref.Key, ref.Lock),
				Fix:     "remove it from your individual file, or ask the team to unlock it"})
		}
	}
	if stateDir != "" {
		if _, err := config.ReadCache(filepath.Join(stateDir, "config.cache"), version); err == nil {
			add(Check{Name: "configuration cache", Status: OK, Message: "fresh"})
		} else {
			add(Check{Name: "configuration cache", Status: Info, Message: "not built for this version yet; hooks read the configuration files directly until it is"})
		}
		t.Mark("read configuration cache")
	}

	pinned := checkPin(ws.Owner, version, add)
	checkLauncher(ws.Owner, add)
	if pinned != "" {
		cache := filepath.Join(cacheHome(), "agent-workspace", "bin", pinned, "aw")
		if info, err := os.Stat(cache); err == nil && info.Mode()&0o111 != 0 {
			add(Check{Name: "cached binary", Status: OK, Message: cache})
		} else {
			add(Check{Name: "cached binary", Status: Info, Message: fmt.Sprintf("aw %s is not cached yet; bin/aw downloads and verifies it on first use", pinned)})
		}
	}

	if stateDir != "" {
		checkState(stateDir, add)
		t.Mark("open state")
	}
	checkIgnored(ws.Owner, c, add)
	checkHooksPath(ws.Owner, c, add)
	checkInstructions(ws.Owner, add)
	checkMachine(ws.Owner, c, add)
	checkDevcontainers(ws.Owner, c, add)
	addMachine(add)
	if withTimings {
		r.Timings = t.Phases()
	}
	return r
}

func keyed(e *config.Error) string {
	if e.Key != "" {
		return e.Key + ": " + e.Msg
	}
	return e.Msg
}

func firstPart(err error) string { m, _, _ := strings.Cut(err.Error(), " → "); return m }

func fixPart(err error, fallback string) string {
	if _, fix, ok := strings.Cut(err.Error(), " → "); ok {
		return fix
	}
	return fallback
}

func cacheHome() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache")
}

// checkPin checks .aw/version and .aw/lock, and returns the pinned version if it is usable.
func checkPin(owner, version string, add func(Check)) string {
	data, err := os.ReadFile(filepath.Join(owner, launcher.VersionPath))
	if err != nil {
		add(Check{Name: "pin", Status: Fail, Message: launcher.VersionPath + " is missing", Fix: "restore it from Git, or run aw init in a new workspace"})
		return ""
	}
	pinned := strings.TrimSpace(string(data))
	if !launcher.Exact(pinned) {
		add(Check{Name: "pin", Status: Fail, Message: fmt.Sprintf("%s is %q, not an exact version", launcher.VersionPath, pinned), Fix: "pin an exact release, such as 1.4.2"})
		return ""
	}
	if pinned == version {
		add(Check{Name: "pin", Status: OK, Message: "aw " + pinned + ", and this is that version"})
	} else {
		add(Check{Name: "pin", Status: Warn, Message: fmt.Sprintf("the workspace pins aw %s, but this is aw %s", pinned, version),
			Fix: "run bin/aw doctor, which runs the pinned version"})
	}

	lock, err := os.ReadFile(filepath.Join(owner, launcher.LockPath))
	if err != nil {
		add(Check{Name: "lock", Status: Fail, Message: launcher.LockPath + " is missing, so bin/aw cannot verify the binary", Fix: "restore it from Git"})
		return pinned
	}
	have := map[string]bool{}
	s := bufio.NewScanner(bytes.NewReader(lock))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			have[f[1]] = true
		}
	}
	here := runtime.GOOS + "_" + runtime.GOARCH
	if !have[launcher.Asset(pinned, here)] {
		add(Check{Name: "lock", Status: Fail, Message: fmt.Sprintf("%s has no checksum for aw %s on %s, so bin/aw will refuse to run here", launcher.LockPath, pinned, here),
			Fix: "restore .aw/lock from Git, or re-pin with the release's checksums.txt"})
		return pinned
	}
	var missing []string
	for _, p := range launcher.Platforms {
		if !have[launcher.Asset(pinned, p)] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		add(Check{Name: "lock", Status: Warn, Message: "no checksum for " + strings.Join(missing, ", ") + ", so bin/aw refuses to run there",
			Fix: "re-pin with the release's complete checksums.txt"})
	} else {
		add(Check{Name: "lock", Status: OK, Message: "checksums for all four platforms"})
	}
	return pinned
}

func checkLauncher(owner string, add func(Check)) {
	path := filepath.Join(owner, launcher.ScriptPath)
	info, err := os.Stat(path)
	switch {
	case err != nil:
		add(Check{Name: "launcher", Status: Fail, Message: launcher.ScriptPath + " is missing", Fix: "restore it from Git"})
		return
	case info.Mode()&0o111 == 0:
		add(Check{Name: "launcher", Status: Fail, Message: launcher.ScriptPath + " is not executable", Fix: "chmod +x bin/aw, and commit the mode"})
		return
	}
	data, _ := os.ReadFile(path)
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	managed, err := os.ReadFile(filepath.Join(owner, ".aw/managed"))
	if err != nil {
		add(Check{Name: "launcher", Status: Warn, Message: ".aw/managed is missing, so hand edits to managed files cannot be detected", Fix: "restore it from Git"})
		return
	}
	for _, line := range strings.Split(string(managed), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == launcher.ScriptPath {
			if f[0] == got {
				add(Check{Name: "launcher", Status: OK, Message: launcher.ScriptPath + " is as aw wrote it"})
			} else {
				add(Check{Name: "launcher", Status: Warn, Message: launcher.ScriptPath + " was edited by hand",
					Fix: "restore it with git checkout bin/aw; aw upgrade will refuse to overwrite an edited launcher"})
			}
			return
		}
	}
	add(Check{Name: "launcher", Status: Warn, Message: ".aw/managed does not list bin/aw", Fix: "restore .aw/managed from Git"})
}

// checkHooksPath reports a repository whose core.hooksPath no longer reaches aw's dispatcher, which
// turns off the protection of its refs (owner-checkout.md). A repository aw setup has not reached
// yet is only noted.
func checkHooksPath(owner string, c *config.Config, add func(Check)) {
	repos := []string{"."}
	if c != nil {
		for _, name := range sortedKeys(c.Projects) {
			if d := c.Projects[name].Directory; d != "." && d != "" {
				repos = append(repos, d)
			}
		}
	}
	configured := func(dir, key string) string {
		out, err := exec.Command("git", "-C", dir, "config", "--local", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	notSet, changed := []string{}, []string{}
	for _, rel := range repos {
		dir := filepath.Join(owner, rel)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		common, err := workspace.GitCommonDir(dir)
		if err != nil {
			continue
		}
		switch {
		case configured(dir, "core.hooksPath") == filepath.Join(common, "aw", "hooks"):
		case configured(dir, "aw.teamHooksPath") == "":
			notSet = append(notSet, rel)
		default:
			changed = append(changed, rel)
		}
	}
	switch {
	case len(changed) > 0:
		add(Check{Name: "git hooks", Status: Warn, Message: "core.hooksPath was changed in " + strings.Join(changed, " ") + ", so aw's ref protection is off there", Fix: "run aw setup"})
	case len(notSet) > 0:
		add(Check{Name: "git hooks", Status: Info, Message: "aw's hooks are not installed in " + strings.Join(notSet, " ") + " yet", Fix: "run aw setup"})
	default:
		add(Check{Name: "git hooks", Status: OK, Message: "every repository runs aw's hooks dispatcher"})
	}
}

// seatPorts are every port a workspace's servers might use: seat 0 to capacity of each.
func seatPorts(c *config.Config) map[int]string {
	out := map[int]string{}
	capacity := map[string]int{}
	for _, r := range c.Resources {
		for _, s := range r.Servers {
			capacity[s] = r.Capacity
		}
	}
	for _, pname := range sortedKeys(c.Projects) {
		for _, sname := range sortedKeys(c.Projects[pname].Servers) {
			full, n := pname+"."+sname, c.Lanes.ServerCapacity
			if v, ok := capacity[full]; ok {
				n = v
			}
			for seat := 0; seat <= n; seat++ {
				out[c.Projects[pname].Servers[sname].Port+c.Lanes.PortStride*seat+c.Ports.Offset] = full
			}
		}
	}
	return out
}

func memoryNeeded(c *config.Config) uint64 {
	var total uint64
	for _, r := range c.Resources {
		capacity := r.Capacity
		if len(r.Tokens) > 0 {
			capacity = len(r.Tokens)
		}
		total += parseSize(r.Memory) * uint64(capacity)
	}
	return total
}

// parseSize reads an approximate size such as 12GB or 512MB, and 0 for anything else.
func parseSize(s string) uint64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, u := range []struct {
		suffix string
		bytes  uint64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil || v < 0 {
				return 0
			}
			return uint64(v * float64(u.bytes))
		}
	}
	return 0
}

// checkMachine compares this workspace with the others registered on the machine (S15): ports they
// could both use, and whether all their seats together would fit in memory.
func checkMachine(owner string, c *config.Config, add func(Check)) {
	if c == nil {
		return
	}
	data, _ := os.ReadFile(config.RegistryFile())
	mine := seatPorts(c)
	need := memoryNeeded(c)
	var clashes []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line == owner {
			continue
		}
		other, err := config.Load(line)
		if err != nil {
			continue // moved or broken: not this workspace's problem
		}
		need += memoryNeeded(other)
		ports := seatPorts(other)
		var shared []int
		for p := range mine {
			if _, ok := ports[p]; ok {
				shared = append(shared, p)
			}
		}
		if len(shared) > 0 {
			slices.Sort(shared)
			clashes = append(clashes, fmt.Sprintf("%s (from port %d, %d shared)", line, shared[0], len(shared)))
		}
	}
	if len(clashes) > 0 {
		add(Check{Name: "ports", Status: Warn, Message: "this workspace's seat ports overlap " + strings.Join(clashes, ", "),
			Fix: "set [ports] offset in your individual layer, so that the two cannot collide"})
	} else if len(mine) > 0 {
		add(Check{Name: "ports", Status: OK, Message: "no overlap with other workspaces on this machine"})
	}
	if total := platform.TotalMemory(); total > 0 && need > total {
		add(Check{Name: "machine capacity", Status: Warn, Message: fmt.Sprintf("the resources of every workspace on this machine could need %.1f GB of memory, and it has %.1f GB", float64(need)/(1<<30), float64(total)/(1<<30)),
			Fix: "lower some capacity, or do not run these at once"})
	}
}

// checkDevcontainers reads each project's devcontainer.json and the CLI's pin. It starts nothing and
// asks no container engine (R2).
func checkDevcontainers(owner string, c *config.Config, add func(Check)) {
	if c == nil {
		return
	}
	any := false
	for _, name := range sortedKeys(c.Projects) {
		if c.Projects[name].Runner != "devcontainer" {
			continue
		}
		any = true
		j, err := runners.Validate(filepath.Join(owner, c.Projects[name].Directory))
		if err != nil {
			add(Check{Name: "devcontainer " + name, Status: Fail, Message: firstPart(err), Fix: fixPart(err, "fix the project's devcontainer.json")})
			continue
		}
		if !runners.ClaimWired(j, name) {
			add(Check{Name: "devcontainer " + name, Status: Warn, Message: "VS Code will not run aw's claim step, so it could reuse a container from a moved checkout",
				Fix: fmt.Sprintf(`set "initializeCommand" to run bin/aw containers claim %s`, name)})
			continue
		}
		add(Check{Name: "devcontainer " + name, Status: OK, Message: "the same path inside and outside, with the seat ports from aw's fragment"})
	}
	if !any {
		return
	}
	if runners.CLIPinned(c.Devcontainer) {
		add(Check{Name: "devcontainer CLI", Status: OK, Message: "pinned: " + strings.ReplaceAll(c.Devcontainer.Command, "{version}", c.Devcontainer.Version)})
	} else {
		add(Check{Name: "devcontainer CLI", Status: Warn, Message: fmt.Sprintf("%q names no version, so builds can change without warning", c.Devcontainer.Command),
			Fix: "name a version in [devcontainer] command, e.g. npx -y @devcontainers/cli@{version}"})
	}
}

func checkInstructions(owner string, add func(Check)) {
	switch d := compose.Drift(owner); d {
	case "":
		add(Check{Name: "instructions", Status: OK, Message: "the generated block in AGENTS.md is current"})
	case "missing":
		add(Check{Name: "instructions", Status: Info, Message: "AGENTS.md has no generated block yet", Fix: "run aw setup"})
	case "stale":
		add(Check{Name: "instructions", Status: Warn, Message: "the generated block in AGENTS.md is out of date", Fix: "run aw setup, and commit the result"})
	case "edited":
		add(Check{Name: "instructions", Status: Warn, Message: "the generated block in AGENTS.md was edited by hand", Fix: "move the change to .aw/instructions.md, delete the block, and run aw setup"})
	default:
		add(Check{Name: "instructions", Status: Warn, Message: "the generated block in AGENTS.md is " + d, Fix: "restore its marker lines, or delete the block, and run aw setup"})
	}
}

func checkState(dir string, add func(Check)) {
	s, err := state.Open(dir, state.ReadOnly)
	var unreadable *state.UnreadableError
	switch {
	case errors.Is(err, state.ErrNoState):
		add(Check{Name: "state", Status: Info, Message: "no state yet: bin/aw setup creates it"})
	case errors.As(err, &unreadable):
		add(Check{Name: "state", Status: Fail, Message: fmt.Sprintf("%s cannot be read (%v), so every lane and lease is treated as protected", unreadable.Path, unreadable.Err),
			Fix: "restore it from the newest state.db.bak-* backup next to it, or move it aside if the workspace has no lanes yet"})
	case err != nil:
		add(Check{Name: "state", Status: Fail, Message: firstPart(err), Fix: fixPart(err, "check the workspace's .git directory")})
	default:
		defer s.Close()
		if s.Newer() {
			add(Check{Name: "state", Status: Warn,
				Message: fmt.Sprintf("state schema %d is newer than this aw's %d, so this aw only reads it and guards refuse", s.Version(), state.SchemaVersion),
				Fix:     "run bin/aw, which runs the pinned version, or upgrade the pin"})
		} else {
			add(Check{Name: "state", Status: OK, Message: fmt.Sprintf("schema %d, %s", s.Version(), s.Path())})
		}
	}
}

func checkIgnored(owner string, c *config.Config, add func(Check)) {
	data, _ := os.ReadFile(filepath.Join(owner, ".gitignore"))
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	want := []string{"/.worktrees/", "/.aw/owner"}
	if c != nil {
		for _, name := range sortedKeys(c.Projects) {
			if d := c.Projects[name].Directory; d != "." && d != "" {
				want = append(want, "/"+d)
			}
		}
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		add(Check{Name: "gitignore", Status: OK, Message: "lanes, the owner pointer and the project repositories are ignored"})
	} else {
		add(Check{Name: "gitignore", Status: Warn, Message: ".gitignore does not list " + strings.Join(missing, " "),
			Fix: "add those lines to .gitignore (without a trailing slash, which would not match a lane's links), so lanes and nested repositories never show up as changes"})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var gitVersionRE = regexp.MustCompile(`git version (\d+)\.(\d+)(?:\.(\d+))?`)

func addMachine(add func(Check)) {
	out, err := gitOutput("--version")
	if err != nil {
		add(Check{Name: "git", Status: Fail, Message: "git is not installed or does not run", Fix: fmt.Sprintf("install Git %d.%d or newer", MinGit[0], MinGit[1])})
	} else if m := gitVersionRE.FindStringSubmatch(out); m == nil {
		add(Check{Name: "git", Status: Warn, Message: "cannot read the Git version from " + strings.TrimSpace(out), Fix: fmt.Sprintf("use Git %d.%d or newer", MinGit[0], MinGit[1])})
	} else {
		v := [3]int{atoi(m[1]), atoi(m[2]), atoi(m[3])}
		if less(v, MinGit) {
			add(Check{Name: "git", Status: Fail, Message: fmt.Sprintf("Git %d.%d.%d is older than %d.%d, which aw needs", v[0], v[1], v[2], MinGit[0], MinGit[1]),
				Fix: fmt.Sprintf("install Git %d.%d or newer", MinGit[0], MinGit[1])})
		} else {
			add(Check{Name: "git", Status: OK, Message: strings.TrimSpace(out)})
		}
	}
	if have("sha256sum") || have("shasum") {
		add(Check{Name: "checksum tool", Status: OK, Message: "bin/aw can verify downloads"})
	} else {
		add(Check{Name: "checksum tool", Status: Fail, Message: "neither sha256sum nor shasum is installed, so bin/aw cannot verify a download", Fix: "install coreutils (sha256sum) or Perl (shasum)"})
	}
	if have("curl") || have("wget") {
		add(Check{Name: "download tool", Status: OK, Message: "bin/aw can download a new version"})
	} else {
		add(Check{Name: "download tool", Status: Warn, Message: "neither curl nor wget is installed, so bin/aw cannot download a version it has not cached", Fix: "install curl"})
	}
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.Output(); close(done) }()
	select {
	case <-done:
		return string(out), err
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", errors.New("git did not answer within 5 s")
	}
}

func have(tool string) bool { _, err := exec.LookPath(tool); return err == nil }

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
