// Command bench measures aw's latency budgets (plans/release-candidate.md §5).
//
//	go run ./bench [-aw path] [-milestone M1] [-mode absolute|relative] [-baseline file] [-out file]
//
// It builds a fixture workspace through aw init and the bin/aw launcher, with the binary cached, then
// runs each budgeted operation as a new process, measuring from just before the process starts to
// its exit. Recording stubs for git, docker, devcontainer, gh, glab, psql, curl and wget are first
// on PATH: any call to one of them from these operations fails the run (§5.3).
//
// In absolute mode an operation fails when its p99 exceeds its budget; in relative mode (macOS CI)
// when its p99 grows more than 25 % over the baseline's. A failing operation is measured once more,
// and only two failures in a row fail the run.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type op struct {
	Name       string `toml:"name"`
	Spec       string `toml:"spec"`
	BudgetMS   int    `toml:"budget_ms"`
	Iterations int    `toml:"iterations"`
	From       string `toml:"from"`
	About      string `toml:"about"`
}

func loadBudgets(path string) ([]op, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Op []op `toml:"op"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Op, nil
}

// Result of one operation.
type Result struct {
	Name     string  `json:"name"`
	N        int     `json:"n"`
	P50      float64 `json:"p50_ms"`
	P95      float64 `json:"p95_ms"`
	P99      float64 `json:"p99_ms"`
	Max      float64 `json:"max_ms"`
	BudgetMS int     `json:"budget_ms"`
	Limit    float64 `json:"limit_ms"` // what p99 was compared against
	Runs     int     `json:"runs"`
	Pass     bool    `json:"pass"`
	Note     string  `json:"note,omitempty"`
}

// Report is the JSON output.
type Report struct {
	GOOS    string    `json:"goos"`
	GOARCH  string    `json:"goarch"`
	Host    string    `json:"host"`
	Mode    string    `json:"mode"`
	Results []*Result `json:"results"`
	Pass    bool      `json:"pass"`
}

func main() {
	awBin := flag.String("aw", "", "the aw binary (default: build it from the repository)")
	milestone := flag.String("milestone", "", "measure every operation from this milestone and earlier (default: acceptance/requirements.toml's current); operations of the milestone in progress run when the bench can run them")
	mode := flag.String("mode", "absolute", "absolute (fail when p99 is over budget) or relative (fail when p50 is 25 % over -baseline-aw's)")
	baselineAW := flag.String("baseline-aw", "", "a baseline aw binary (master's), measured interleaved with -aw on this machine")
	out := flag.String("out", "", "write the JSON report here")
	scale := flag.Float64("scale", 1, "multiply every operation's iterations, e.g. 0.1 for a quick run")
	flag.Parse()

	ops, err := loadBudgets("bench/budgets.toml")
	check(err)
	upTo, inProgress, err := milestones(*milestone)
	check(err)

	tmp, err := os.MkdirTemp("", "aw-bench-")
	check(err)
	defer os.RemoveAll(tmp)
	fx, err := newFixture(tmp, *awBin)
	check(err)

	var baseFx *fixture
	if *baselineAW != "" {
		dir := filepath.Join(tmp, "baseline")
		check(os.MkdirAll(dir, 0o755))
		baseFx, err = newFixture(dir, *baselineAW)
		check(err)
	}
	if *mode == "relative" && baseFx == nil {
		fatalf("relative mode needs -baseline-aw")
	}

	rep := &Report{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Host: host(), Mode: *mode, Pass: true}
	for _, o := range ops {
		m, err := milestoneNumber(o.From)
		check(err)
		run, ok := fx.ops[o.Name]
		switch {
		case m <= upTo && !ok:
			fatalf("budgets.toml lists %s from %s, which has been reached, but the bench cannot run it", o.Name, o.From)
		case m > upTo && (m != inProgress || !ok):
			continue
		}
		var baseRun runner
		if baseFx != nil {
			baseRun = baseFx.ops[o.Name] // nil when the baseline predates the operation
		}
		n := max(10, int(float64(o.Iterations)**scale))
		r, b := measure(o, n, run, baseRun)
		judge(o, r, b, *mode)
		if !r.Pass {
			// One noisy run on a shared runner cannot fail CI on its own (§5.2).
			r2, b2 := measure(o, n, run, baseRun)
			judge(o, r2, b2, *mode)
			r2.Runs = 2
			if !r2.Pass {
				r2.Note += fmt.Sprintf("; failed twice (first run p50 %.2f, p99 %.2f ms)", r.P50, r.P99)
			}
			r = r2
		}
		if !r.Pass {
			rep.Pass = false
		}
		rep.Results = append(rep.Results, r)
	}
	if calls := fx.stubCalls(); calls != "" {
		rep.Pass = false
		fmt.Fprintf(os.Stderr, "bench: these operations called tools the hot path and read-only commands may not use (speed.md):\n%s", calls)
	}

	printSummary(os.Stdout, rep)
	if *out != "" {
		data, _ := json.MarshalIndent(rep, "", "  ")
		check(os.WriteFile(*out, data, 0o644))
	}
	if !rep.Pass {
		os.Exit(1)
	}
}

// judge decides one result. Absolute: p99 within the budget, with growth of more than 25 % over
// the baseline flagged. Relative: p50 within 25 % of the baseline's, measured in the same run; the
// median is used because the tail of a shared virtual machine is mostly noise from its neighbours.
func judge(o op, r, b *Result, mode string) {
	switch mode {
	case "absolute":
		r.Limit = float64(o.BudgetMS)
		r.Pass = r.P99 <= r.Limit
		if b != nil && b.P99 > 0 {
			change := (r.P99/b.P99 - 1) * 100
			r.Note = fmt.Sprintf("p99 %+.0f %% vs master's %.2f ms", change, b.P99)
			if change > 25 {
				r.Note += "; flagged: more than 25 % over master"
			}
		}
	case "relative":
		if b == nil {
			r.Pass, r.Note = true, "master has no such operation yet; reported only"
			return
		}
		r.Limit = b.P50 * 1.25
		r.Pass = r.P50 <= r.Limit
		r.Note = fmt.Sprintf("p50 vs master's %.2f ms + 25 %%", b.P50)
	default:
		fatalf("unknown mode %q", mode)
	}
}

// measure runs an operation n times, after a warm-up. With a baseline, the two alternate, so that
// drift on a shared runner affects both alike.
func measure(o op, n int, run, base runner) (*Result, *Result) {
	for range min(50, n/5) {
		run()
		if base != nil {
			base()
		}
	}
	var mine, theirs []float64
	timeIt := func(f runner, into *[]float64) {
		start := time.Now()
		f()
		*into = append(*into, float64(time.Since(start).Microseconds())/1000)
	}
	for range n {
		timeIt(run, &mine)
		if base != nil {
			timeIt(base, &theirs)
		}
	}
	r := summarize(o, mine)
	if base == nil {
		return r, nil
	}
	return r, summarize(o, theirs)
}

func summarize(o op, samples []float64) *Result {
	slices.Sort(samples)
	return &Result{Name: o.Name, N: len(samples), P50: percentile(samples, 50), P95: percentile(samples, 95),
		P99: percentile(samples, 99), Max: samples[len(samples)-1], BudgetMS: o.BudgetMS, Runs: 1}
}

func percentile(sorted []float64, p int) float64 {
	i := (len(sorted)*p+99)/100 - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func printSummary(w io.Writer, rep *Report) {
	fmt.Fprintf(w, "### Latency budgets on %s/%s (%s), %s mode\n\n", rep.GOOS, rep.GOARCH, rep.Host, rep.Mode)
	fmt.Fprintln(w, "| Operation | n | p50 ms | p95 ms | p99 ms | max ms | limit ms | result |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|")
	for _, r := range rep.Results {
		res := "pass"
		if !r.Pass {
			res = "**FAIL**"
		}
		limit := "—"
		if r.Limit > 0 {
			limit = fmt.Sprintf("%.2f", r.Limit)
		}
		if r.Note != "" {
			res += " (" + r.Note + ")"
		}
		fmt.Fprintf(w, "| %s | %d | %.2f | %.2f | %.2f | %.2f | %s | %s |\n", r.Name, r.N, r.P50, r.P95, r.P99, r.Max, limit, res)
	}
	fmt.Fprintln(w)
}

func host() string {
	if h := os.Getenv("AW_BENCH_HOST"); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// milestones returns the milestone whose operations must all run (flag, or requirements.toml's
// current) and the one in progress (-1 when the flag is given).
func milestones(flagValue string) (int, int, error) {
	if flagValue != "" {
		m, err := milestoneNumber(flagValue)
		return m, -1, err
	}
	data, err := os.ReadFile("requirements.toml")
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		Current    string `toml:"current"`
		InProgress string `toml:"in_progress"`
	}
	if err := toml.Unmarshal(data, &r); err != nil {
		return 0, 0, err
	}
	cur, err := milestoneNumber(r.Current)
	if err != nil {
		return 0, 0, err
	}
	next := -1
	if r.InProgress != "" {
		if next, err = milestoneNumber(r.InProgress); err != nil {
			return 0, 0, err
		}
	}
	return cur, next, nil
}

func milestoneNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(s, "M"))
	if err != nil || !strings.HasPrefix(s, "M") {
		return 0, fmt.Errorf("%q is not a milestone", s)
	}
	return n, nil
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench: "+format+"\n", args...)
	os.Exit(2)
}

// The fixture.

type runner func()

type fixture struct {
	owner   string
	env     []string
	stubLog string
	ops     map[string]runner
}

// stubbed are the tools the measured operations may not run (speed.md, "The hot path").
var stubbed = []string{"git", "docker", "devcontainer", "gh", "glab", "psql", "curl", "wget"}

const testVersion = "0.0.0-bench"

func newFixture(tmp, awBin string) (*fixture, error) {
	if awBin == "" {
		awBin = filepath.Join(tmp, "aw")
		build := exec.Command("go", "build", "-trimpath", "-ldflags",
			"-X github.com/connor981332/agent-workspace/internal/cli.version="+testVersion, "-o", awBin, "./cmd/aw")
		build.Dir = ".."
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building aw: %v\n%s", err, out)
		}
	}
	versionOut, err := exec.Command(awBin, "version", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("%s version --json: %v", awBin, err)
	}
	var v struct{ Version string }
	if err := json.Unmarshal(versionOut, &v); err != nil {
		return nil, err
	}

	mirror := filepath.Join(tmp, "mirror")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(awBin)
	if err != nil {
		return nil, err
	}
	asset := fmt.Sprintf("aw_%s_%s_%s", v.Version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(mirror, asset), data, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(mirror, "checksums.txt"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		return nil, err
	}

	home := filepath.Join(tmp, "home")
	owner := filepath.Join(tmp, "bench workspace")
	env := append(os.Environ(),
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"), "AW_MIRROR="+mirror)
	if err := os.MkdirAll(owner, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Bench\n\temail = bench@example.invalid\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		return nil, err
	}
	runIn := func(dir string, name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v: %v\n%s", name, args, err, out)
		}
		return nil
	}
	// Four project repositories, as in §5.1's fixture.
	if err := runIn(owner, "git", "init", "-q"); err != nil {
		return nil, err
	}
	for _, p := range []string{"web", "api", "android", "ios"} {
		dir := filepath.Join(owner, p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := runIn(dir, "git", "init", "-q"); err != nil {
			return nil, err
		}
		if err := runIn(dir, "git", "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
			return nil, err
		}
	}
	if err := runIn(owner, awBin, "init"); err != nil {
		return nil, err
	}
	// aw init compiled the configuration cache. The M1 operations read only the cache, so the
	// generated configuration (four projects) is enough. M2 adds lanes, resources and hooks here,
	// and a state-changing command to rebuild the cache after editing workspace.toml.
	launcher := filepath.Join(owner, "bin", "aw")
	if err := runIn(owner, launcher, "version"); err != nil { // fetch, verify and cache the binary
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(owner, ".git", "aw", "config.cache")); err != nil {
		return nil, fmt.Errorf("aw init wrote no configuration cache: %v", err)
	}

	stubs := filepath.Join(tmp, "stubs")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		return nil, err
	}
	stubLog := filepath.Join(tmp, "stub.log")
	for _, s := range stubbed {
		script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\nexit 97\n", s, stubLog)
		if err := os.WriteFile(filepath.Join(stubs, s), []byte(script), 0o755); err != nil {
			return nil, err
		}
	}
	measured := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "PATH=") {
			measured = append(measured, e)
		}
	}
	measured = append(measured, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))

	payload, _ := json.Marshal(map[string]any{
		"session_id": "bench-session", "cwd": owner, "hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]string{"command": `cd web && npm run test -- --grep "checkout tax" 2>&1 | tee /tmp/out.log; git status --porcelain`},
	})
	fx := &fixture{owner: owner, env: measured, stubLog: stubLog}
	cmd := func(stdin []byte, args ...string) runner {
		return func() {
			c := exec.Command(launcher, args...)
			c.Dir, c.Env = owner, fx.env
			if stdin != nil {
				c.Stdin = bytes.NewReader(stdin)
			}
			var errOut bytes.Buffer
			c.Stderr = &errOut
			if err := c.Run(); err != nil {
				fatalf("%v: %v\n%s", args, err, errOut.String())
			}
		}
	}
	fx.ops = map[string]runner{
		"hook":       cmd(payload, "hook", "claude", "pre-tool-use"),
		"whoami":     cmd(nil, "whoami"),
		"config-get": cmd(nil, "config", "get", "lanes.max"),
	}
	return fx, nil
}

func (fx *fixture) stubCalls() string {
	data, _ := os.ReadFile(fx.stubLog)
	return string(data)
}
