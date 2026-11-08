package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRefusals(t *testing.T) {
	dir := t.TempDir()
	_, err := Init(InitOptions{Dir: dir, Version: "1.2.3", Mirror: "/nowhere"})
	if err == nil || !strings.Contains(err.Error(), "not a Git repository") {
		t.Fatalf("non-repository: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Init(InitOptions{Dir: dir, Version: "dev", Mirror: "/nowhere"})
	if err == nil || !strings.Contains(err.Error(), "a development build that cannot be pinned") {
		t.Fatalf("development build: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a refused init wrote files: %v", entries)
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"shop-workspace": "shopworkspace", "RideTracker": "ridetracker", "2026 work": "work", "!!!": "workspace",
		"averyveryverylongworkspacename": "averyveryverylongwor"} {
		if got := deriveNamespace(in); got != want {
			t.Errorf("deriveNamespace(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"web": "web", "Web App": "web-app", "api_v2": "api-v2", "3d": "p-3d", "--x--": "x"} {
		if got := projectName(in); got != want {
			t.Errorf("projectName(%q) = %q, want %q", in, got, want)
		}
	}
}
