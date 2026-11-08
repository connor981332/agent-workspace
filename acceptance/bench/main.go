// Command bench measures aw's latency budgets (plans/release-candidate.md §5).
//
//	go run ./bench [-aw path] [-milestone M1] [-mode absolute|relative] [-baseline file] [-out file]
//
// It builds a fixture workspace through aw init and the bin/aw launcher, with the binary cached, then
// runs each budgeted operation as a new process, measuring from just before the process starts to
// its exit (for aw new, to the moment the stub harness starts). Recording stubs for git, docker,
// devcontainer, gh, glab, psql, curl and wget are first on PATH: any call to one of them from the
// hot-path operations fails the run (§5.3). The lane operations read and change worktrees, so for
// them Git is real and only the others are stubbed.
//
// In absolute mode, for a maintainer's laptop, an operation fails when its p99 exceeds its budget.
// In relative mode, for CI, it fails when its median grows more than 25 % over the baseline's, and
// with -guard-budgets the operations marked ci_absolute must also meet their budget. A failing
// operation is measured once more, and only two failures in a row fail the run.
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
	CIAbsolute bool   `toml:"ci_absolute"` // the budget also holds on CI, with -guard-budgets (speed.md)
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
	guardBudgets := flag.Bool("guard-budgets", false, "in relative mode, also fail a ci_absolute operation whose p99 is over its budget")
	flag.Parse()

	ops, err := loadBudgets("bench/budgets.toml")
	check(err)
	upTo, inProgress, err := milestones(*milestone)
	check(err)

	// On the disk users have: /tmp is often a RAM disk, where a sync costs nothing, and SQLite's syncs
	// are most of what a state-changing command spends (docs/dev/benchmarks.md).
	base, err := os.UserCacheDir()
	check(err)
	check(os.MkdirAll(base, 0o755))
	tmp, err := os.MkdirTemp(base, "aw-bench-")
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
		var baseRun *benchOp
		if baseFx != nil {
			baseRun = baseFx.ops[o.Name] // nil when the baseline predates the operation
		}
		n := max(10, int(float64(o.Iterations)**scale))
		r, b := measure(o, n, run, baseRun)
		judge(o, r, b, *mode, *guardBudgets)
		if !r.Pass {
			// One noisy run on a shared runner cannot fail CI on its own (§5.2).
			r2, b2 := measure(o, n, run, baseRun)
			judge(o, r2, b2, *mode, *guardBudgets)
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
		os.RemoveAll(tmp) // os.Exit skips the deferred one
		os.Exit(1)
	}
}

// judge decides one result. Absolute: p99 within the budget, with growth of more than 25 % over
// the baseline flagged. Relative: p50 within 25 % of the baseline's, measured in the same run; the
// median is used because the tail of a shared virtual machine is mostly noise from its neighbours.
// A ci_absolute operation barely touches the disk, so its budget can also hold there.
func judge(o op, r, b *Result, mode string, guardBudgets bool) {
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
		} else {
			r.Limit = b.P50 * 1.25
			r.Pass = r.P50 <= r.Limit
			r.Note = fmt.Sprintf("p50 vs master's %.2f ms + 25 %%", b.P50)
		}
		if guardBudgets && o.CIAbsolute && r.P99 > float64(o.BudgetMS) {
			r.Pass = false
			r.Note += fmt.Sprintf("; p99 is over its %d ms budget", o.BudgetMS)
		}
	default:
		fatalf("unknown mode %q", mode)
	}
}

// measure runs an operation n times, after a warm-up. With a baseline, the two alternate, so that
// drift on a shared runner affects both alike.
func measure(o op, n int, run, base *benchOp) (*Result, *Result) {
	for _, b := range []*benchOp{run, base} {
		if b != nil && b.prepare != nil {
			b.prepare()
			b.prepare = nil // a second measurement (after a failure) reuses it
		}
	}
	for range min(50, n/5) {
		run.once()
		if base != nil {
			base.once()
		}
	}
	var mine, theirs []float64
	timeIt := func(b *benchOp, into *[]float64) {
		*into = append(*into, float64(b.once().Microseconds())/1000)
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

// benchOp is one measured operation: run returns how long it took. prepare, when set, runs once
// before the first iteration, untimed; reset, when set, runs after each iteration, untimed, to put
// the fixture back (a claimed lane returned to the pool).
type benchOp struct {
	prepare func()
	run     func() time.Duration
	reset   func()
}

func (b *benchOp) once() time.Duration {
	d := b.run()
	if b.reset != nil {
		b.reset()
	}
	return d
}

type fixture struct {
	owner   string
	env     []string // the hot-path environment: every tool stubbed
	laneEnv []string // the lane operations' environment: real Git, the rest stubbed
	stubLog string
	ops     map[string]*benchOp
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
	// Not this process's harness identity: run from inside a harness, every aw attach would be one
	// session, and would get the same lane.
	inherited := slices.DeleteFunc(os.Environ(), func(e string) bool {
		for _, p := range []string{"AW_SESSION", "CLAUDE_CODE_SESSION_ID=", "CODEX_THREAD_ID=", "GROK_SESSION_ID=", "OPENCODE_SESSION_ID="} {
			if strings.HasPrefix(e, p) {
				return true
			}
		}
		return false
	})
	env := append(inherited,
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"), "AW_MIRROR="+mirror,
		"AW_ANCESTRY_STOP="+strconv.Itoa(os.Getpid())) // a harness above the bench is not a session's
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
	laneStubs := filepath.Join(tmp, "lane-stubs")
	if err := os.MkdirAll(laneStubs, 0o755); err != nil {
		return nil, err
	}
	for _, s := range stubbed[1:] { // all but git
		if err := os.Link(filepath.Join(stubs, s), filepath.Join(laneStubs, s)); err != nil {
			return nil, err
		}
	}
	laneEnv := append(slices.Clone(measured), "PATH="+laneStubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	measured = append(measured, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))

	payload, _ := json.Marshal(map[string]any{
		"session_id": "bench-session", "cwd": owner, "hook_event_name": "PreToolUse", "tool_name": "Bash",
		// A compound read-only command, which a session in the owner checkout may run: the guard
		// parses it, checks every command and redirect, and allows it.
		"tool_input": map[string]string{"command": `cd web && rg -n "checkout tax" src 2>/dev/null | head -20; git status --porcelain; git log --oneline -5`},
	})
	fx := &fixture{owner: owner, env: measured, laneEnv: laneEnv, stubLog: stubLog}
	timed := func(env []string, stdin []byte, args ...string) func() time.Duration {
		return func() time.Duration {
			start := time.Now()
			fx.aw(env, stdin, args...)
			return time.Since(start)
		}
	}
	fx.ops = map[string]*benchOp{
		"hook":       {run: timed(measured, payload, "hook", "claude", "pre-tool-use")},
		"whoami":     {run: timed(measured, nil, "whoami")},
		"config-get": {run: timed(measured, nil, "config", "get", "lanes.max")},
	}
	if err := fx.addLaneOps(tmp, launcher); err != nil {
		// The baseline's binary may predate lanes: its lane operations are then not compared.
		if !isBaseline(tmp) {
			return nil, err
		}
		fmt.Fprintf(os.Stderr, "bench: the baseline cannot run the lane operations (%v); they are reported without it\n", firstLine(err))
	}
	return fx, nil
}

// aw runs bin/aw in the owner checkout, failing the bench if it fails.
func (fx *fixture) aw(env []string, stdin []byte, args ...string) {
	if err := fx.try(env, stdin, args...); err != nil {
		fatalf("%v", err)
	}
}

// output runs bin/aw in the owner checkout and returns its standard output.
func (fx *fixture) output(env []string, args ...string) ([]byte, error) {
	c := exec.Command(filepath.Join(fx.owner, "bin", "aw"), args...)
	c.Dir, c.Env = fx.owner, env
	var errOut bytes.Buffer
	c.Stderr = &errOut
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("aw %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return out, nil
}

func (fx *fixture) try(env []string, stdin []byte, args ...string) error {
	c := exec.Command(filepath.Join(fx.owner, "bin", "aw"), args...)
	c.Dir, c.Env = fx.owner, env
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	var errOut bytes.Buffer
	c.Stderr = &errOut
	if err := c.Run(); err != nil {
		return fmt.Errorf("aw %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return nil
}

func isBaseline(tmp string) bool { return filepath.Base(tmp) == "baseline" }

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}

// laneCount and pool shape the M2 fixture: §5.1's ten lanes across four repositories, each with
// unmerged work, half of them with uncommitted work too, and a warm pool to claim from.
const (
	laneCount = 10
	poolSize  = 2
)

// addResourceOps adds the operations of the resource queue (M4). A lane is claimed once for each
// role, the first time it is needed.
func (fx *fixture) addResourceOps(env []string, launcher string, signals *os.File) {
	lane := func(purpose string) func() string {
		root := ""
		return func() string {
			if root != "" {
				return root
			}
			out, err := fx.output(env, "attach", purpose, "--json")
			if err != nil {
				fatalf("%v", err)
			}
			var claimed struct{ Root string }
			if err := json.Unmarshal(out, &claimed); err != nil || claimed.Root == "" {
				fatalf("aw attach --json: no root in %s", out)
			}
			root = claimed.Root
			return root
		}
	}
	in := func(dir string, args ...string) error {
		c := exec.Command(launcher, args...)
		c.Dir, c.Env = dir, env
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("aw %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
		return nil
	}
	must := func(dir string, args ...string) {
		if err := in(dir, args...); err != nil {
			fatalf("%v", err)
		}
	}
	// untilSignal times a command up to the byte its server writes to the FIFO.
	untilSignal := func(dir string, args ...string) time.Duration {
		c := exec.Command(launcher, args...)
		c.Dir, c.Env = dir, env
		var errOut bytes.Buffer
		c.Stderr = &errOut
		start := time.Now()
		if err := c.Start(); err != nil {
			fatalf("aw %v: %v", args, err)
		}
		got := make(chan time.Duration, 1)
		go func() {
			var b [1]byte
			if _, err := io.ReadFull(signals, b[:]); err != nil {
				fatalf("reading the server's signal: %v", err)
			}
			got <- time.Since(start)
		}()
		var d time.Duration
		select {
		case d = <-got:
		case <-time.After(20 * time.Second):
			fatalf("aw %v: no signal from the server\n%s", args, errOut.String())
		}
		if err := c.Wait(); err != nil {
			fatalf("aw %v: %v\n%s", args, err, errOut.String())
		}
		return d
	}
	serving := lane("bench serve")
	fx.ops["serve-free-seat"] = &benchOp{run: func() time.Duration {
		return untilSignal(serving(), "serve", "bench-serve")
	}, reset: func() { must(serving(), "stop", "bench-serve") }}

	holder, next := lane("bench holder"), lane("bench next")
	fx.ops["grant-next"] = &benchOp{prepare: func() { must(holder(), "serve", "bench-token") }, run: func() time.Duration {
		_ = in(next(), "serve", "bench-token") // exits 3: it joins the queue
		c := exec.Command(launcher, "stop", "bench-token")
		c.Dir, c.Env = holder(), append(slices.Clone(env), "AW_DEBUG=1")
		start := time.Now()
		out, err := c.CombinedOutput()
		d := time.Since(start)
		if err != nil {
			fatalf("aw stop: %v\n%s", err, out)
		}
		if os.Getenv("AW_BENCH_PHASES") != "" {
			fmt.Fprintf(os.Stderr, "phases grant-next %.1fms %s\n", float64(d.Microseconds())/1000, strings.TrimSpace(string(out)))
		}
		return d
	}, reset: func() {
		must(next(), "stop", "bench-token")
		must(holder(), "serve", "bench-token")
	}}

	victim := lane("bench victim")
	fx.ops["preempt"] = &benchOp{prepare: func() { must(victim(), "serve", "bench-evict") }, run: func() time.Duration {
		return untilSignal(fx.owner, "serve", "bench-evict")
	}, reset: func() {
		must(fx.owner, "stop", "bench-evict")
		must(victim(), "serve", "bench-evict")
	}}
}

// slowSample is how long a lane operation may take before the bench says where the time went, so a
// CI failure can be told apart from a slow runner.
const slowSample = 100 * time.Millisecond

func reportSlow(op string, d time.Duration, stderr string) {
	if d < slowSample {
		return
	}
	load, _ := os.ReadFile("/proc/loadavg")
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.Contains(l, "timings:") {
			lines = append(lines, strings.TrimPrefix(l, "aw: timings: "))
		}
	}
	fmt.Fprintf(os.Stderr, "slow %s %.1fms load=%s %s\n", op, float64(d.Microseconds())/1000, strings.TrimSpace(string(load)), strings.Join(lines, " | "))
}

// addLaneOps builds the lanes and adds the dashboard, attach-from-pool and new-until-harness
// operations. Each lane operation's reset returns the claimed lane to the pool with aw sweep.
func (fx *fixture) addLaneOps(tmp, launcher string) error {
	toml := filepath.Join(fx.owner, "workspace.toml")
	data, err := os.ReadFile(toml)
	if err != nil {
		return err
	}
	// The servers signal a FIFO when they start, or when a lane's copy receives SIGTERM.
	fifo := filepath.Join(tmp, "harness.fifo")
	settings := fmt.Sprintf(`
[lanes]
max = %d
pool = %d

[projects.web.servers.benchserve]
command = "printf x > \"$AW_BENCH_FIFO\"; exec sleep 300"
port = 43000

[projects.web.servers.benchvictim]
command = "trap '[ -n \"$AW_LANE\" ] && printf x > \"$AW_BENCH_FIFO\"; exit 0' TERM; sleep 300 & wait $!"
port = 43500

[resources.bench-serve]
kind = "process"
capacity = 1
servers = ["web.benchserve"]

[resources.bench-evict]
kind = "process"
capacity = 1
servers = ["web.benchvictim"]
owner_counts = true
owner_preempts = true
evict_grace = "1s"

[resources.bench-token]
kind = "token"
tokens = ["t"]
`, laneCount+poolSize+8, poolSize)
	fx.laneEnv = append([]string{"AW_BENCH_FIFO=" + fifo}, fx.laneEnv...)
	if strings.Contains(string(data), "\n[lanes]\n") {
		return fmt.Errorf("aw init wrote a [lanes] table; the bench must merge into it")
	}
	if err := os.WriteFile(toml, append(data, settings...), 0o644); err != nil {
		return err
	}
	env := fx.laneEnv
	git := func(dir string, args ...string) error {
		c := exec.Command("git", args...)
		c.Dir, c.Env = dir, env
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v in %s: %v\n%s", args, dir, err, out)
		}
		return nil
	}
	// As a maintainer would after aw init: commit what it wrote.
	if err := git(fx.owner, "add", "-A"); err != nil {
		return err
	}
	if err := git(fx.owner, "commit", "-q", "-m", "Add Agent Workspace"); err != nil {
		return err
	}
	if err := fx.try(env, nil, "recap", "--json"); err != nil {
		return err // no dashboard in this binary
	}
	// The pool: lanes claimed and released at once, which aw sweep recycles.
	for i := range poolSize {
		if err := fx.try(env, nil, "attach", fmt.Sprintf("pool lane %d", i)); err != nil {
			return err
		}
	}
	refill := func() { fx.aw(env, nil, "sweep", "--apply") }
	refill()

	// aw new execs the harness. The stub harness writes one byte to a FIFO, so the time is taken
	// when the harness starts, not when it exits.
	if err := mkfifo(fifo); err != nil {
		return err
	}
	// Held open for reading and writing for the whole run: the harness's open never blocks, and
	// each iteration reads exactly its one byte, never waiting for end of file (which kqueue can
	// miss on a FIFO on macOS).
	signals, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	harnessDir := filepath.Join(tmp, "harness")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		return err
	}
	stub := fmt.Sprintf("#!/bin/sh\nprintf x > %q\n", fifo)
	if err := os.WriteFile(filepath.Join(harnessDir, "benchharness"), []byte(stub), 0o755); err != nil {
		return err
	}
	newEnv := append(slices.Clone(env[:len(env)-1]), env[len(env)-1]+string(os.PathListSeparator)+harnessDir)
	n := 0
	// The dashboard's ten lanes are built only when it is measured, after attach and new, so that
	// their resets (aw sweep) see only the pool.
	buildLanes := func() {
		for i := 1; i <= laneCount; i++ {
			out, err := fx.output(env, "attach", fmt.Sprintf("bench lane %d", i), "--json")
			if err != nil {
				fatalf("%v", err)
			}
			var claimed struct{ Root string }
			if err := json.Unmarshal(out, &claimed); err != nil || claimed.Root == "" {
				fatalf("aw attach --json: no root in %s", out)
			}
			root := claimed.Root
			for _, p := range []string{"web", "api", "android", "ios"} {
				dir := filepath.Join(root, p)
				if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("work\n"), 0o644); err != nil {
					fatalf("%v", err)
				}
				if err := git(dir, "add", "work.txt"); err != nil {
					fatalf("%v", err)
				}
				if err := git(dir, "commit", "-q", "-m", "work"); err != nil {
					fatalf("%v", err)
				}
			}
			if i%2 == 1 { // half the lanes have uncommitted work; the others are clean, so their proof is tried
				if err := os.WriteFile(filepath.Join(root, "web", "dirty.txt"), []byte("wip\n"), 0o644); err != nil {
					fatalf("%v", err)
				}
			}
		}
	}
	fx.ops["dashboard"] = &benchOp{prepare: buildLanes, run: func() time.Duration {
		start := time.Now()
		fx.aw(env, nil)
		return time.Since(start)
	}}
	// The ref hook, in a lane of its own. A binary that predates it refuses every state.
	if fx.try(fx.env, []byte{}, "git-hook", "reference-transaction", "committed") == nil {
		var laneDir, update string
		fx.ops["ref-hook"] = &benchOp{prepare: func() {
			out, err := fx.output(env, "attach", "bench ref hook", "--json")
			if err != nil {
				fatalf("%v", err)
			}
			var claimed struct{ Root, Name string }
			if err := json.Unmarshal(out, &claimed); err != nil || claimed.Root == "" {
				fatalf("aw attach --json: no root in %s", out)
			}
			laneDir = claimed.Root
			zero, next := strings.Repeat("0", 40), strings.Repeat("a", 40)
			update = zero + " " + next + " refs/heads/aw/" + claimed.Name + "\n"
		}, run: func() time.Duration {
			c := exec.Command(launcher, "git-hook", "reference-transaction", "prepared")
			c.Dir, c.Env = laneDir, fx.env // every tool stubbed: the hook may not run one
			c.Stdin = strings.NewReader(update)
			var errOut bytes.Buffer
			c.Stderr = &errOut
			start := time.Now()
			if err := c.Run(); err != nil {
				fatalf("aw git-hook: %v\n%s", err, errOut.String())
			}
			return time.Since(start)
		}}
	}
	if fx.try(env, nil, "queue", "--json") == nil {
		fx.addResourceOps(env, launcher, signals)
	}
	fx.ops["attach-from-pool"] = &benchOp{run: func() time.Duration {
		n++
		c := exec.Command(filepath.Join(fx.owner, "bin", "aw"), "attach", fmt.Sprintf("bench attach %d", n))
		c.Dir, c.Env = fx.owner, append(slices.Clone(env), "AW_DEBUG=1")
		var errOut bytes.Buffer
		c.Stderr = &errOut
		start := time.Now()
		if err := c.Run(); err != nil {
			fatalf("aw attach: %v\n%s", err, errOut.String())
		}
		d := time.Since(start)
		reportSlow("attach-from-pool", d, errOut.String())
		return d
	}, reset: refill}
	fx.ops["new-until-harness"] = &benchOp{run: func() time.Duration {
		n++
		c := exec.Command(launcher, "new", fmt.Sprintf("bench new %d", n), "--harness", "benchharness")
		c.Dir, c.Env = fx.owner, append(slices.Clone(newEnv), "AW_DEBUG=1")
		var errOut bytes.Buffer
		c.Stderr = &errOut
		start := time.Now()
		if err := c.Start(); err != nil {
			fatalf("aw new: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		started := make(chan time.Duration, 1)
		go func() {
			var b [1]byte
			if _, err := io.ReadFull(signals, b[:]); err != nil {
				fatalf("reading the harness's signal: %v", err)
			}
			started <- time.Since(start)
		}()
		select {
		case d := <-started:
			if err := <-done; err != nil {
				fatalf("aw new: %v\n%s", err, errOut.String())
			}
			reportSlow("new-until-harness", d, errOut.String())
			return d
		case err := <-done:
			// The harness writes and exits at once, so both may be ready together.
			select {
			case d := <-started:
				if err == nil {
					reportSlow("new-until-harness", d, errOut.String())
					return d
				}
			case <-time.After(10 * time.Second):
			}
			fatalf("aw new exited before the harness started: %v\n%s", err, errOut.String())
		}
		return 0
	}, reset: refill}
	return nil
}

func (fx *fixture) stubCalls() string {
	data, _ := os.ReadFile(fx.stubLog)
	return string(data)
}
