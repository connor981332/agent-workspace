package evidence

import "testing"

func TestAllowed(t *testing.T) {
	gen := []string{"node_modules", "tmp/", "*.pyc", "build/cache"}
	ours := []string{".env", ".aw/owner"}
	for _, e := range []string{"node_modules/", "node_modules/x/i.js", "tmp/", "tmp/a", "x.pyc", ".env", ".aw/owner", "build/cache/", "src/x.pyc"} {
		if !allowed(e, gen, ours) {
			t.Errorf("%q not allowed", e)
		}
	}
	for _, e := range []string{"node_modules2/", "debug.log", "build/", ".envrc", ".aw/", "src/x.py"} {
		if allowed(e, gen, ours) {
			t.Errorf("%q allowed", e)
		}
	}
}
