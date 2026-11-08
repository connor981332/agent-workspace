// Command bench measures the T3 prototype hot path (plans/release-candidate.md §5, §12 T3).
//
//	go run ./bench -n 1000 -out results.json
//
// It builds each variant, builds a fixture workspace per variant (a lane root whose .aw/owner points
// at the owner checkout, a cached binary, a state database and a config cache), then runs each
// variant as a new process, round-robin, measuring from just before the process starts to its exit.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type variant struct {
	Name     string
	Pkg      string
	Tags     string
	Ldflags  string
	Launcher bool // run through bin/aw in a lane root
	bin      string
	argv     []string
	env      []string
}

type result struct {
	Name      string             `json:"name"`
	N         int                `json:"n"`
	P50       float64            `json:"p50_ms"`
	P95       float64            `json:"p95_ms"`
	P99       float64            `json:"p99_ms"`
	Max       float64            `json:"max_ms"`
	SizeBytes int64              `json:"binary_bytes"`
	Phases    map[string]float64 `json:"phases_us,omitempty"`
	InitTop   []initCost         `json:"init_top,omitempty"`
	InitTotal float64            `json:"init_total_ms,omitempty"`
}

type initCost struct {
	Pkg string  `json:"pkg"`
	Ms  float64 `json:"ms"`
}

// A 200-character command of the kind agents run.
const command = `cd web && npm run test -- --grep "checkout tax" 2>&1 | tee /tmp/out.log; git status --porcelain && echo "$(git rev-parse --abbrev-ref HEAD)" > .aw-branch && rg -n 'TODO' src/ | head -n 20`

func main() {
	n := flag.Int("n", 1000, "iterations per variant")
	out := flag.String("out", "", "write JSON results here")
	keep := flag.Bool("keep", false, "keep the fixture directory")
	flag.Parse()

	tmp, err := os.MkdirTemp("", "aw-bench-")
	check(err)
	if !*keep {
		defer os.RemoveAll(tmp)
	}
	launcher, err := filepath.Abs("launcher.sh")
	check(err)
	cache := filepath.Join(tmp, "cache")

	variants := []*variant{
		{Name: "floor (Go process, direct)", Pkg: "./cmd/floor"},
		{Name: "floor via launcher", Pkg: "./cmd/floor", Launcher: true},
		{Name: "prototype, direct", Pkg: "./cmd/proto"},
		{Name: "prototype via launcher (modernc)", Pkg: "./cmd/proto", Launcher: true},
		{Name: "prototype + cobra linked, via launcher", Pkg: "./cmd/proto", Tags: "withcli", Launcher: true},
		{Name: "prototype via launcher (ncruces)", Pkg: "./cmd/proto", Tags: "ncruces", Launcher: true},
		{Name: "prototype stripped (-s -w), via launcher", Pkg: "./cmd/proto", Ldflags: "-s -w", Launcher: true},
		{Name: "fallback: session snapshot file, no SQLite, stripped, via launcher", Pkg: "./cmd/proto", Tags: "snapshot", Ldflags: "-s -w", Launcher: true},
	}
	for i, v := range variants {
		version := fmt.Sprintf("v0.0.0-spike.%d", i)
		v.bin = filepath.Join(cache, "agent-workspace", "bin", version, "aw")
		check(os.MkdirAll(filepath.Dir(v.bin), 0o755))
		build := exec.Command("go", "build", "-trimpath", "-tags", v.Tags, "-ldflags", v.Ldflags, "-o", v.bin, v.Pkg)
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := build.CombinedOutput(); err != nil {
			fatalf("build %s: %v\n%s", v.Name, err, b)
		}

		owner := filepath.Join(tmp, fmt.Sprintf("ws %d", i))
		lane := filepath.Join(owner, ".worktrees", "03")
		check(os.MkdirAll(filepath.Join(owner, ".aw"), 0o755))
		run(owner, "git", "init", "-q")
		run(owner, "git", "-c", "user.name=b", "-c", "user.email=b@example.invalid", "commit", "-q", "--allow-empty", "-m", "init")
		run(owner, "git", "worktree", "add", "-q", "-b", "lane-03", lane)
		check(os.WriteFile(filepath.Join(owner, ".aw", "version"), []byte(version+"\n"), 0o644))
		for _, root := range []string{owner, lane} {
			check(os.MkdirAll(filepath.Join(root, "bin"), 0o755))
			data, err := os.ReadFile(launcher)
			check(err)
			check(os.WriteFile(filepath.Join(root, "bin", "aw"), data, 0o755))
		}
		check(os.MkdirAll(filepath.Join(lane, ".aw"), 0o755))
		check(os.WriteFile(filepath.Join(lane, ".aw", "owner"), []byte(owner+"\n"), 0o644))
		if v.Pkg == "./cmd/proto" {
			run(owner, v.bin, "setup", owner)
		}
		v.env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
		if v.Launcher {
			v.argv = []string{filepath.Join(lane, "bin", "aw"), "hook", "claude", "pre-tool-use"}
		} else {
			v.argv = []string{v.bin, "hook", "claude", "pre-tool-use"}
		}
		p, _ := json.Marshal(map[string]any{
			"session_id": "session-03", "cwd": lane, "hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]string{"command": command},
		})
		v.env = append(v.env, "AW_BENCH_PAYLOAD="+string(p))
		// The decision must come out right before anything is timed.
		got := once(v, false)
		if !bytes.Contains(got, []byte(`"allow"`)) {
			fatalf("%s: unexpected output %s", v.Name, got)
		}
	}

	// Warm up, then measure round-robin so that drift on a shared runner affects every variant
	// alike.
	for range 50 {
		for _, v := range variants {
			once(v, false)
		}
	}
	samples := make([][]time.Duration, len(variants))
	for range *n {
		for i, v := range variants {
			start := time.Now()
			once(v, false)
			samples[i] = append(samples[i], time.Since(start))
		}
	}

	var results []result
	for i, v := range variants {
		s := samples[i]
		slices.Sort(s)
		info, err := os.Stat(v.bin)
		check(err)
		r := result{Name: v.Name, N: len(s), P50: pct(s, 50), P95: pct(s, 95), P99: pct(s, 99),
			Max: ms(s[len(s)-1]), SizeBytes: info.Size()}
		if v.Pkg == "./cmd/proto" {
			r.Phases = phaseAverages(v)
		}
		r.InitTop, r.InitTotal = initTrace(v)
		results = append(results, r)
	}

	fmt.Printf("### Hot-path spike on %s/%s (%s), n = %d\n\n", runtime.GOOS, runtime.GOARCH, hostName(), *n)
	fmt.Println("| Variant | p50 ms | p95 ms | p99 ms | max ms | binary MB | init ms |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, r := range results {
		fmt.Printf("| %s | %.2f | %.2f | %.2f | %.2f | %.1f | %.2f |\n", r.Name, r.P50, r.P95, r.P99, r.Max,
			float64(r.SizeBytes)/1e6, r.InitTotal)
	}
	for _, r := range results {
		if r.Phases == nil {
			continue
		}
		fmt.Printf("\n%s, in-process phases (mean µs):", r.Name)
		keys := make([]string, 0, len(r.Phases))
		for k := range r.Phases {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf(" %s %.0f,", k, r.Phases[k])
		}
		fmt.Printf("\n  heaviest package inits:")
		for _, c := range r.InitTop {
			fmt.Printf(" %s %.3f ms,", c.Pkg, c.Ms)
		}
		fmt.Println()
	}
	if *out != "" {
		data, _ := json.MarshalIndent(map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "host": hostName(), "results": results}, "", "  ")
		check(os.WriteFile(*out, data, 0o644))
	}
}

func once(v *variant, timings bool) []byte {
	cmd := exec.Command(v.argv[0], v.argv[1:]...)
	var payload string
	for _, e := range v.env {
		if p, ok := strings.CutPrefix(e, "AW_BENCH_PAYLOAD="); ok {
			payload = p
		}
	}
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = v.env
	if timings {
		cmd.Env = append(cmd.Env, "AW_SPIKE_TIMINGS=1")
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		fatalf("%s: %v\n%s", v.Name, err, errOut.String())
	}
	if timings {
		return errOut.Bytes()
	}
	return out.Bytes()
}

var phaseRE = regexp.MustCompile(`([a-z]+)=([0-9]+)`)

func phaseAverages(v *variant) map[string]float64 {
	sum := map[string]float64{}
	const runs = 200
	for range runs {
		for _, m := range phaseRE.FindAllStringSubmatch(string(once(v, true)), -1) {
			x, _ := strconv.ParseFloat(m[2], 64)
			sum[m[1]] += x
		}
	}
	for k := range sum {
		sum[k] /= runs
	}
	return sum
}

var initRE = regexp.MustCompile(`^init (\S+) @[0-9.]+ ms, ([0-9.]+) ms clock`)

// initTrace runs the binary once with GODEBUG=inittrace=1 and returns the costliest package
// initializations and their total.
func initTrace(v *variant) ([]initCost, float64) {
	cmd := exec.Command(v.bin, "hook", "claude", "pre-tool-use")
	cmd.Env = append(slices.Clone(v.env), "GODEBUG=inittrace=1")
	var payload string
	for _, e := range v.env {
		if p, ok := strings.CutPrefix(e, "AW_BENCH_PAYLOAD="); ok {
			payload = p
		}
	}
	cmd.Stdin = strings.NewReader(payload)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	_ = cmd.Run()
	var costs []initCost
	var total float64
	s := bufio.NewScanner(&errOut)
	for s.Scan() {
		if m := initRE.FindStringSubmatch(s.Text()); m != nil {
			x, _ := strconv.ParseFloat(m[2], 64)
			costs = append(costs, initCost{m[1], x})
			total += x
		}
	}
	slices.SortFunc(costs, func(a, b initCost) int {
		switch {
		case a.Ms > b.Ms:
			return -1
		case a.Ms < b.Ms:
			return 1
		}
		return 0
	})
	if len(costs) > 6 {
		costs = costs[:6]
	}
	return costs, total
}

func pct(sorted []time.Duration, p int) float64 {
	i := (len(sorted)*p+99)/100 - 1
	if i < 0 {
		i = 0
	}
	return ms(sorted[i])
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func hostName() string {
	if h := os.Getenv("AW_BENCH_HOST"); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

func run(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		fatalf("%s %v: %v\n%s", name, args, err, b)
	}
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench: "+format+"\n", args...)
	os.Exit(1)
}
