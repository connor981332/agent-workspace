package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExact(t *testing.T) {
	for _, v := range []string{"1.4.2", "0.0.0-acceptance", "1.5.0-rc.1", "1.5.0+build.7", "10.20.30"} {
		if !Exact(v) {
			t.Errorf("%q is exact", v)
		}
	}
	for _, v := range []string{"", "v1.4.2", "1.4", "1.4.x", "^1.4.2", "latest", "main", "1.4.2 ", "1.4.2.1"} {
		if Exact(v) {
			t.Errorf("%q is not exact", v)
		}
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	sum := strings.Repeat("a", 64)
	data := sum + "  aw_1.2.3_linux_amd64\n" + sum + "  aw_1.2.3_darwin_arm64\n" + sum + "  aw_1.2.4_linux_arm64\nnot a line\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, platforms, err := Lock(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(platforms, ",") != "linux_amd64,darwin_arm64" || strings.Count(string(lock), "\n") != 2 {
		t.Errorf("lock %q, platforms %v", lock, platforms)
	}
	if _, _, err := Lock(dir, "9.9.9"); err == nil {
		t.Errorf("a version with no binaries was locked")
	}
	if _, _, err := Lock("relative/dir", "1.2.3"); err == nil {
		t.Errorf("a relative mirror was accepted")
	}
}
