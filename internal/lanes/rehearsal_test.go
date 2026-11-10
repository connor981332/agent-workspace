package lanes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
)

func TestCheckIsolated(t *testing.T) {
	write := func(root, rel, text string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Projects: map[string]*config.Project{"api": {Directory: "api", Runner: "devcontainer"}}}

	bad := t.TempDir()
	write(bad, "infra/compose.yaml", "name: rt-infra\nservices:\n  db:\n    container_name: db\n    ports:\n      - \"5432:5432\"\nnetworks:\n  default:\n    name: rt-net\n    external: true\n")
	write(bad, "api/.devcontainer/compose.yaml", "services:\n  api:\n    ports:\n      - 3050:3050\n")
	write(bad, "bin/lane-bucket", "docker ps --filter project=rt-infra\n")
	write(bad, "docs/notes.md", "rt-infra is mentioned here\n")
	got := strings.Join(checkIsolated(bad, cfg), "\n")
	for _, want := range []string{"infra/compose.yaml:6: a fixed host port", "infra/compose.yaml:4: a fixed container name", "a fixed shared network", "api/.devcontainer/compose.yaml:4: a fixed host port", "bin/lane-bucket:1: names rt-infra"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "docs/notes.md") {
		t.Errorf("documentation counted:\n%s", got)
	}

	good := t.TempDir()
	write(good, "infra/compose.yaml", "name: rt-infra\nservices:\n  db:\n    ports:\n      - \"${AW_PORT_5432:-5432}:5432\"\nnetworks:\n  default:\n    name: ${AW_INFRA_NETWORK:-rt-net}\n    external: true\n")
	write(good, "bin/lane-bucket", "docker ps --filter project=\"${AW_INFRA_PROJECT:-rt-infra}\"\n")
	if got := checkIsolated(good, cfg); len(got) != 0 {
		t.Errorf("an isolated proposal was refused: %v", got)
	}
}
