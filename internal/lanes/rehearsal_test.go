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
	problems, _ := checkIsolated(bad, "", cfg)
	got := strings.Join(problems, "\n")
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
	if got, _ := checkIsolated(good, "", cfg); len(got) != 0 {
		t.Errorf("an isolated proposal was refused: %v", got)
	}
}

func TestCheckIsolatedHostProjects(t *testing.T) {
	write := func(root, rel, text string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Projects: map[string]*config.Project{"web": {Directory: "web", Runner: "host"}},
		Lanes: config.Lanes{Data: map[string]*config.Data{
			"files": {Provider: "external", Command: "scripts/files"},
			"far":   {Provider: "external", Command: "some-program-on-path"},
		}},
	}
	bad := t.TempDir()
	write(bad, "web/docker-compose.yml", "name: shopfront\nservices:\n  db:\n    ports:\n      - \"5432:5432\"\n")
	write(bad, "web/bin/dev", "docker compose -p shopfront up\n")
	write(bad, "web/src/app.txt", "welcome to shopfront\n")
	write(bad, "web/testdata/compose.yaml", "services:\n  x:\n    ports:\n      - \"1:1\"\n")
	write(bad, "scripts/files", "curl localhost:3900\ndocker ps --filter project=shop-infra\n")
	problems, _ := checkIsolated(bad, "shop", cfg)
	got := strings.Join(problems, "\n")
	for _, want := range []string{"web/docker-compose.yml:5: a fixed host port", "web/bin/dev:1: names shopfront", "scripts/files:1: a fixed address", "scripts/files:2: names shop-infra", "not a file in the workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"app.txt", "testdata"} {
		if strings.Contains(got, not) {
			t.Errorf("%s counted:\n%s", not, got)
		}
	}
	delete(cfg.Lanes.Data, "far")
	good := t.TempDir()
	write(good, "web/compose.yaml", "services:\n  db:\n    ports:\n      - \"${AW_PORT_5432:-5432}:5432\"\n")
	write(good, "scripts/files", "docker ps --filter project=\"${AW_INFRA_PROJECT:-shop-infra}\"\n")
	if got, _ := checkIsolated(good, "shop", cfg); len(got) != 0 {
		t.Errorf("an isolated proposal was refused: %v", got)
	}

	// A reviewed file's fixed ports are skipped and listed; its Compose project's name is still
	// refused beside a container command, since that would reach the real one.
	cfg.Projects["web"].Accepted = map[string]string{"docker-compose.yml": "opt-in backing services, never started by a lane"}
	problems, skipped := checkIsolated(bad, "shop", cfg)
	got = strings.Join(problems, "\n")
	if strings.Contains(got, "web/docker-compose.yml:5") || !strings.Contains(got, "web/bin/dev:1: names shopfront") {
		t.Errorf("an accepted file:\n%s", got)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "web: docker-compose.yml (opt-in backing services") {
		t.Errorf("skipped: %v", skipped)
	}
}

func TestHostComposeEnv(t *testing.T) {
	dir := t.TempDir()
	if env := hostComposeEnv(dir, "rh1", "web", 2, 20000); env != nil {
		t.Errorf("a project with no Compose file got %v", env)
	}
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  db:\n    ports:\n      - \"${AW_PORT_5432:-5432}:5432\"\n"), 0o644)
	got := strings.Join(hostComposeEnv(dir, "rh1", "Web.App", 2, 20000), " ")
	for _, want := range []string{"AW_PORT_5432=25432", "COMPOSE_PROJECT_NAME=rh1-web-app-2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: ${AW_INFRA_PROJECT:-web}\nservices: {}\n"), 0o644)
	if got := strings.Join(hostComposeEnv(dir, "rh1", "web", 2, 20000), " "); strings.Contains(got, "COMPOSE_PROJECT_NAME") {
		t.Errorf("a variable name was overridden: %s", got)
	}
}
