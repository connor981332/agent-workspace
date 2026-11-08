// Package archtest checks the dependency rules in plans/release-candidate.md §4.3 with go list, so
// that they hold without anyone remembering them.
package archtest

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/connor981332/agent-workspace"

// Leaves import only other leaves (rule 1).
var leaves = []string{
	"internal/config", "internal/state", "internal/workspace", "internal/gitx", "internal/gitx/run",
	"internal/extproto", "internal/platform", "internal/ui",
}

// The hot path (rule 3). Subpackages are included, except the ones in hotPathExcluded, which exist
// so that subprocess work has somewhere to live off the hot path.
var (
	hotPath = []string{
		"internal/harness", "internal/policy", "internal/shell", "internal/config", "internal/state",
		"internal/sessions", "internal/workspace", "internal/gitx",
	}
	hotPathExcluded = []string{"internal/harness/launch", "internal/gitx/run"}
)

// forbiddenOnHotPath may not be imported by a hot-path package, directly or transitively.
var forbiddenOnHotPath = []string{"os/exec", "net", "net/http"}

// entryPoint is cmd/aw. It links the whole command tree (cobra's flag library imports net, and the
// CLI will reach os/exec through app), so rule 3 holds for its own imports only. Its dispatch keeps
// the command tree off the hot path at run time (rule 4).
const entryPoint = "cmd/aw"

type pkg struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func listPackages(t *testing.T) []pkg {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", module+"/...")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("go list: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list found no packages")
	}
	return pkgs
}

// rel returns the path inside the module, or "" for a package outside it.
func rel(importPath string) string {
	if importPath == module {
		return "."
	}
	if r, ok := strings.CutPrefix(importPath, module+"/"); ok {
		return r
	}
	return ""
}

// under reports whether p is one of roots or a subpackage of one.
func under(p string, roots []string) bool {
	return slices.ContainsFunc(roots, func(r string) bool { return p == r || strings.HasPrefix(p, r+"/") })
}

func isHotPath(p string) bool { return under(p, hotPath) && !under(p, hotPathExcluded) }

func TestLeavesImportOnlyLeaves(t *testing.T) {
	for _, p := range listPackages(t) {
		from := rel(p.ImportPath)
		if !under(from, leaves) {
			continue
		}
		for _, imp := range p.Imports {
			if to := rel(imp); to != "" && !under(to, leaves) {
				t.Errorf("leaf %s imports %s, which is not a leaf", from, to)
			}
		}
	}
}

func TestComponentsDoNotImportAppOrCLI(t *testing.T) {
	for _, p := range listPackages(t) {
		from := rel(p.ImportPath)
		if from == entryPoint || under(from, []string{"internal/cli"}) {
			continue
		}
		for _, imp := range p.Imports {
			to := rel(imp)
			if under(to, []string{"internal/cli"}) || (under(to, []string{"internal/app"}) && !under(from, []string{"internal/app"})) {
				t.Errorf("%s imports %s; flows across components belong in internal/app", from, to)
			}
		}
	}
}

func TestHotPathHasNoSubprocessOrNetwork(t *testing.T) {
	for _, p := range listPackages(t) {
		from := rel(p.ImportPath)
		switch {
		case from == entryPoint:
			for _, bad := range forbiddenOnHotPath {
				if slices.Contains(p.Imports, bad) {
					t.Errorf("%s imports %s directly; move that code behind the command tree", from, bad)
				}
			}
		case isHotPath(from):
			for _, bad := range forbiddenOnHotPath {
				if slices.Contains(p.Deps, bad) {
					t.Errorf("hot-path package %s depends on %s (directly or transitively)", from, bad)
				}
			}
		}
	}
}

// TestRulesSeeTheTree guards the test itself: if the module path or layout changed and go list
// stopped matching, every rule above would pass vacuously.
func TestRulesSeeTheTree(t *testing.T) {
	var found bool
	for _, p := range listPackages(t) {
		if rel(p.ImportPath) == entryPoint {
			found = true
		}
	}
	if !found {
		t.Fatalf("go list did not report %s/%s", module, entryPoint)
	}
}

func TestClassification(t *testing.T) {
	cases := map[string]bool{
		"internal/gitx":           true,
		"internal/gitx/run":       false,
		"internal/harness/claude": true,
		"internal/harness/launch": false,
		"internal/harnessx":       false,
		"internal/cli":            false,
		"cmd/aw":                  false,
	}
	for p, want := range cases {
		if got := isHotPath(p); got != want {
			t.Errorf("isHotPath(%q) = %v, want %v", p, got, want)
		}
	}
}
