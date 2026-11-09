package acceptance_test

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestE2E runs the end-to-end suite against real Docker containers (plans/release-candidate.md §7).
// It runs only when AW_E2E=1, in the e2e-docker job. Each scenario gets containers of its own, named
// in E2E_PG and E2E_REDIS, which are removed afterwards.
func TestE2E(t *testing.T) {
	if os.Getenv("AW_E2E") != "1" {
		t.Skip("set AW_E2E=1 to run the end-to-end suite, which needs Docker")
	}
	files, err := filepath.Glob("e2e/*.txtar")
	if err != nil || len(files) == 0 {
		t.Fatalf("no end-to-end scenarios: %v", err)
	}
	p := params(files)
	setup := p.Setup
	p.Setup = func(env *testscript.Env) error {
		if err := setup(env); err != nil {
			return err
		}
		id := make([]byte, 6)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		pg, redis := "aw-e2e-pg-"+hex.EncodeToString(id), "aw-e2e-redis-"+hex.EncodeToString(id)
		env.Setenv("E2E_PG", pg)
		env.Setenv("E2E_REDIS", redis)
		env.Defer(func() { exec.Command("docker", "rm", "-f", pg, redis).Run() })
		return nil
	}
	testscript.Run(t, p)
}
