package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefAllowed(t *testing.T) {
	common := t.TempDir()
	const (
		zero = "0000000000000000000000000000000000000000"
		a    = "1111111111111111111111111111111111111111"
		b    = "2222222222222222222222222222222222222222"
	)
	if err := os.MkdirAll(filepath.Join(common, "refs/heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	for ref, oid := range map[string]string{"refs/heads/loose": a, "refs/heads/both": a} {
		if err := os.WriteFile(filepath.Join(common, ref), []byte(oid+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packed := "# pack-refs with: peeled fully-peeled sorted\n" + a + " refs/heads/both\n" + a + " refs/heads/packed\n^" + b + "\n"
	if err := os.WriteFile(filepath.Join(common, "packed-refs"), []byte(packed), 0o644); err != nil {
		t.Fatal(err)
	}
	own := []string{"refs/heads/aw/mine"}
	for _, tc := range []struct {
		old, new, ref string
		want          bool
	}{
		{a, b, "refs/heads/aw/mine", true},
		{zero, a, "refs/heads/aw/mine", true},
		{a, b, "HEAD", true},
		{a, zero, "AUTO_MERGE", true},
		{a, b, "ORIG_HEAD", true},
		{zero, a, "refs/bisect/bad", true},
		{zero, a, "refs/remotes/origin/main", true},
		{a, a, "refs/heads/main", true},
		// Foreign refs.
		{a, b, "refs/heads/main", false},
		{zero, b, "refs/heads/new-branch", false},
		{a, b, "refs/tags/v1", false},
		{zero, b, "refs/stash", false},
		{a, zero, "refs/stash", false},
		{zero, b, "refs/notes/commits", false},
		{zero, b, "refs/aw/archive/x", false},
		// git pack-refs: written to packed-refs at its current value, then the loose copy removed.
		{zero, a, "refs/heads/loose", true},
		{a, zero, "refs/heads/both", true},
		{zero, a, "refs/heads/packed", true},
		// branch -f and branch -D are not packing.
		{zero, b, "refs/heads/loose", false},
		{zero, zero + "x", "refs/heads/loose", false},
		{a, zero, "refs/heads/loose", false},     // loose only: a real deletion
		{a, zero, "refs/heads/packed", false},    // packed only: a real deletion
		{zero, zero, "refs/heads/packed", false}, // git branch -D: the old value is not reported
		{zero, zero, "refs/heads/loose", false},
		{zero, zero, "refs/tags/v1", false},
		{zero, zero, "refs/heads/aw/mine", true},
	} {
		if got := refAllowed(tc.old, tc.new, tc.ref, own, common); got != tc.want {
			t.Errorf("%s %s %s: %v, want %v", tc.old[:4], tc.new[:4], tc.ref, got, tc.want)
		}
	}
}
