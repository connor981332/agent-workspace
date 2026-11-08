package acceptance_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoImportsFromTheMainModule keeps the suite black-box (§3.2): it may not import any package of
// the implementation it tests.
func TestNoImportsFromTheMainModule(t *testing.T) {
	const main = "github.com/connor981332/agent-workspace"
	const self = main + "/acceptance"
	out, err := exec.Command("go", "list", "-deps", "-test", "./...").Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go list: %v\n%s", err, stderr)
	}
	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg, _, _ = strings.Cut(pkg, " ") // test variants print as "p [p.test]"
		// The suite's own packages, including external test packages (…/acceptance_test).
		if pkg == self || strings.HasPrefix(pkg, self+"/") || pkg == self+"_test" || pkg == self+".test" {
			continue
		}
		if pkg == main || strings.HasPrefix(pkg, main+"/") {
			t.Errorf("the acceptance suite imports %s from the main module", pkg)
		}
	}
}
