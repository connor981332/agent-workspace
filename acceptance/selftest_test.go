package acceptance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestHarness runs the scenarios in testdata/harness, which test the suite's own commands. A file
// named *-fails.txtar must fail, and its failure must match its "# want-failure:" line. This is how
// the suite proves that a broken owner-unchanged check fails a scenario (T2).
func TestHarness(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "harness", "*.txtar"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no harness scenarios: %v", err)
	}
	stage := t.TempDir()
	for _, src := range files {
		name := strings.TrimSuffix(filepath.Base(src), ".txtar")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(stage, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name+".txtar")
			if err := os.WriteFile(path, []byte(withFinalCheck(string(data))), 0o644); err != nil {
				t.Fatal(err)
			}

			rec := &recordingT{}
			testscript.RunT(rec, params([]string{path}))
			wantFail := strings.HasSuffix(name, "-fails")
			switch {
			case wantFail && !rec.failed:
				t.Fatalf("the scenario passed, but it must fail\n%s", rec.log.String())
			case !wantFail && rec.failed:
				t.Fatalf("the scenario failed\n%s", rec.log.String())
			case wantFail:
				want := wantFailure(string(data))
				if want == "" {
					t.Fatalf("a -fails scenario needs a '# want-failure:' line")
				}
				if !strings.Contains(rec.log.String(), want) {
					t.Fatalf("the scenario failed, but not with %q\n%s", want, rec.log.String())
				}
			}
		})
	}
}

func wantFailure(script string) string {
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(line, "# want-failure:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// recordingT is a testscript.T that records a failure instead of failing the test.
type recordingT struct {
	mu     sync.Mutex
	failed bool
	log    strings.Builder
}

type failNow struct{}

func (r *recordingT) Skip(args ...any) { r.Log(append([]any{"SKIP:"}, args...)...); panic(failNow{}) }
func (r *recordingT) Fatal(args ...any) {
	r.Log(args...)
	r.FailNow()
}
func (r *recordingT) Parallel() {}
func (r *recordingT) Log(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(&r.log, args...)
}
func (r *recordingT) FailNow() {
	r.mu.Lock()
	r.failed = true
	r.mu.Unlock()
	panic(failNow{})
}
func (r *recordingT) Run(name string, f func(testscript.T)) {
	defer func() {
		if e := recover(); e != nil {
			if _, ok := e.(failNow); !ok {
				panic(e)
			}
		}
	}()
	f(r)
}
func (r *recordingT) Verbose() bool { return false }
