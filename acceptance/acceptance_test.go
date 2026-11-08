// The acceptance suite (plans/release-candidate.md §3). It treats aw as an executable only: this
// module never imports the main module (imports_test.go checks that).
//
//	go test ./...                       build aw from the repository and run every scenario
//	AW_BIN=/path/to/aw go test ./...    run against a given binary
//	go test -run 'TestScenarios/cli/'   run one area
//	AW_ACCEPT_MILESTONE=M1 go test ./...  also run the scenarios of a later milestone
package acceptance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/connor981332/agent-workspace/acceptance/cmds"
	"github.com/connor981332/agent-workspace/acceptance/scenario"
)

var cfg cmds.Config

func TestMain(m *testing.M) {
	code, err := setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance:", err)
		os.Exit(2)
	}
	if code == 0 {
		code = m.Run()
	}
	os.Exit(code)
}

func setup() (int, error) {
	tmp, err := os.MkdirTemp("", "aw-acceptance-")
	if err != nil {
		return 0, err
	}
	// The temporary directory is left for the OS to clean, so a failed run can be inspected.

	awBin := os.Getenv("AW_BIN")
	if awBin == "" {
		awBin = filepath.Join(tmp, "bin", "aw")
		build := exec.Command("go", "build", "-trimpath",
			"-ldflags", "-X github.com/connor981332/agent-workspace/internal/cli.version="+testVersion, "-o", awBin, "./cmd/aw")
		build.Dir = ".."
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("building aw: %v\n%s", err, out)
		}
	}
	if awBin, err = filepath.Abs(awBin); err != nil {
		return 0, err
	}

	version, err := binaryVersion(awBin)
	if err != nil {
		return 0, err
	}
	mirror, err := makeMirror(filepath.Join(tmp, "mirror"), awBin, version)
	if err != nil {
		return 0, err
	}

	globalAW := filepath.Join(tmp, "global")
	if err := os.MkdirAll(globalAW, 0o755); err != nil {
		return 0, err
	}
	// The test tools (acceptance/tools) go next to the global aw, on every scenario's PATH.
	tools, err := filepath.Glob(filepath.Join("tools", "*"))
	if err != nil {
		return 0, err
	}
	for _, tool := range tools {
		build := exec.Command("go", "build", "-o", filepath.Join(globalAW, filepath.Base(tool)), "./"+tool)
		if out, err := build.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("building %s: %v\n%s", tool, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(globalAW, "aw"), []byte(globalShim), 0o755); err != nil {
		return 0, err
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		return 0, fmt.Errorf("the acceptance suite needs git: %v", err)
	}
	stubs, err := filepath.Abs("stubs")
	if err != nil {
		return 0, err
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		return 0, err
	}
	cfg = cmds.Config{AWBin: awBin, GlobalAW: globalAW, StubsDir: stubs, RealGit: realGit, Repo: repo,
		Version: version, Mirror: mirror}
	return 0, nil
}

// testVersion is the version the runner gives the aw it builds, so that aw init can pin it.
const testVersion = "0.0.0-acceptance"

// binaryVersion asks the binary under test for its version.
func binaryVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("%s version --json: %v", bin, err)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.Version == "" {
		return "", fmt.Errorf("%s version --json printed %q", bin, out)
	}
	return v.Version, nil
}

// makeMirror lays out a release mirror (AW_MIRROR) holding the binary under test for this
// platform, as GoReleaser names it, with its checksums.txt.
func makeMirror(dir, bin, version string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return "", err
	}
	asset := fmt.Sprintf("aw_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(dir, asset), data, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)
	return dir, os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(line), 0o644)
}

// globalShim is the suite's global aw. Inside a workspace it runs that workspace's bin/aw launcher,
// so scenarios go through the launcher as users do; elsewhere it runs the binary under test. M7
// builds this proxy into aw itself (distribution.md), and the shim is then replaced by the binary.
const globalShim = `#!/bin/sh
d=$PWD
while [ -n "$d" ]; do
	if [ -x "$d/bin/aw" ] && [ -f "$d/.aw/version" ]; then
		exec "$d/bin/aw" "$@"
	fi
	[ "$d" = / ] && break
	d=$(dirname "$d")
done
exec "$AW_BIN" "$@"
`

func TestScenarios(t *testing.T) {
	reqs, err := scenario.LoadRequirements("requirements.toml")
	if err != nil {
		t.Fatal(err)
	}
	current, err := reqs.CurrentMilestone()
	if err != nil {
		t.Fatal(err)
	}
	inProgress := reqs.InProgressMilestone()
	all, err := scenario.Find("scenarios")
	if err != nil {
		t.Fatal(err)
	}

	// One testscript run per area, so that subtests are named TestScenarios/<area>/<name>.
	stage := t.TempDir()
	type area struct {
		files   []string
		pending []scenario.Scenario
	}
	areas := map[string]*area{}
	var order []string
	var running, waiting int
	for _, sc := range all {
		name, _, _ := strings.Cut(sc.Name, "/")
		a := areas[name]
		if a == nil {
			a = &area{}
			areas[name] = a
			order = append(order, name)
		}
		if sc.Milestone > current && !(sc.Milestone == inProgress && !sc.Unwritten) {
			a.pending = append(a.pending, sc)
			waiting++
			continue
		}
		a.files = append(a.files, stageScenario(t, stage, sc))
		running++
	}
	t.Logf("%d scenarios run, %d pending (current milestone %s)", running, waiting, current)

	for _, name := range order {
		a := areas[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, sc := range a.pending {
				_, base, _ := strings.Cut(sc.Name, "/")
				t.Run(base, func(t *testing.T) {
					if sc.Unwritten {
						t.Skipf("pending: not written yet; must pass from %s (current milestone %s)", sc.Milestone, current)
					}
					t.Skipf("pending: must pass from %s (current milestone %s)", sc.Milestone, current)
				})
			}
			if len(a.files) > 0 {
				testscript.Run(t, params(a.files))
			}
		})
	}
}

func params(files []string) testscript.Params {
	return testscript.Params{
		Files:               files,
		Setup:               cmds.Setup(cfg),
		Cmds:                cmds.Commands(),
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	}
}

// stageScenario copies a scenario into dir, named so that the subtest is <area>/<name>, and appends
// the automatic owner-checkout check to its script.
func stageScenario(t *testing.T, dir string, sc scenario.Scenario) string {
	t.Helper()
	data, err := os.ReadFile(sc.Path)
	if err != nil {
		t.Fatal(err)
	}
	staged := withFinalCheck(string(data))
	area, name, _ := strings.Cut(sc.Name, "/")
	areaDir := filepath.Join(dir, area)
	if err := os.MkdirAll(areaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(areaDir, name+".txtar")
	if err := os.WriteFile(path, []byte(staged), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// withFinalCheck inserts the final check at the end of the script, before the first archive file.
func withFinalCheck(txtar string) string {
	script, files := txtar, ""
	if strings.HasPrefix(txtar, "-- ") {
		script, files = "", txtar
	} else if i := strings.Index(txtar, "\n-- "); i >= 0 {
		script, files = txtar[:i+1], txtar[i+1:]
	}
	if script != "" && !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	return script + "\n# Added by the runner: the owner checkout must be unchanged.\n" + cmds.FinalCheck + "\n" + files
}
