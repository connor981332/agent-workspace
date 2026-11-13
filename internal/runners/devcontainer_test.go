package runners

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

func TestStripJSONC(t *testing.T) {
	in := `{
  // a comment with "quotes" and a , comma
  "url": "http://x//y", /* block , */
  "s": "a,]",
  "list": ["a", "b", /* trailing */ ],
  "n": {"k": 1,},
}`
	var got map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(in)), &got); err != nil {
		t.Fatalf("%v in %s", err, stripJSONC([]byte(in)))
	}
	if got["url"] != "http://x//y" || got["s"] != "a,]" || len(got["list"].([]any)) != 2 {
		t.Fatalf("strings or lists changed: %v", got)
	}
}

func TestComposeName(t *testing.T) {
	for dir, want := range map[string]string{
		"/x/web":           "web_devcontainer",
		"/x/My.App":        "myapp_devcontainer",
		"/x/_odd-Name 2":   "odd-name2_devcontainer",
		"/x/it's a folder": "itsafolder_devcontainer",
	} {
		if got := ComposeName(dir); got != want {
			t.Errorf("ComposeName(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestProjectComposeName(t *testing.T) {
	write := func(dir, name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".devcontainer", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"compose.yaml", "compose.kafka.yaml", Fragment}
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	os.Unsetenv("COMPOSE_PROJECT_NAME")

	plain := filepath.Join(t.TempDir(), "Checkout")
	write(plain, "compose.yaml", "services:\n  app: {}\n")
	if got := ProjectComposeName(plain, files); got != "checkout_devcontainer" {
		t.Errorf("no name: %q", got)
	}

	named := filepath.Join(t.TempDir(), "checkout")
	write(named, "compose.yaml", "name: Monolith\nservices:\n  app: {}\n")
	write(named, Fragment, "name: checkout_devcontainer\n")
	if got := ProjectComposeName(named, files); got != "monolith" {
		t.Errorf("a top-level name, with aw's fragment listed after it: %q", got)
	}
	write(named, "compose.kafka.yaml", "name: \"${STACK:-monolith-dev}\" # the last file wins\n")
	if got := ProjectComposeName(named, files); got != "monolith-dev" {
		t.Errorf("an interpolated name with a default: %q", got)
	}
	write(named, ".env", "STACK=mine\n")
	if got := ProjectComposeName(named, files); got != "mine" {
		t.Errorf("a name from .env: %q", got)
	}
	// COMPOSE_PROJECT_NAME counts from the .env where the CLI runs, the checkout, not .devcontainer.
	write(named, ".env", "STACK=mine\nCOMPOSE_PROJECT_NAME=ignored\n")
	if got := ProjectComposeName(named, files); got != "mine" {
		t.Errorf("COMPOSE_PROJECT_NAME in .devcontainer/.env: %q", got)
	}
	if err := os.WriteFile(filepath.Join(named, ".env"), []byte("COMPOSE_PROJECT_NAME=From.Env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ProjectComposeName(named, files); got != "fromenv" {
		t.Errorf("COMPOSE_PROJECT_NAME in the checkout's .env: %q", got)
	}
	os.Remove(filepath.Join(named, ".env"))
	write(named, ".env", "")
	write(named, "compose.kafka.yaml", "name: ${UNSET_STACK_NAME}\n")
	if got := ProjectComposeName(named, files); got != "checkout_devcontainer" {
		t.Errorf("an unresolvable name falls back: %q", got)
	}
}

func TestHostPathUndoesDockerDesktopPrefixes(t *testing.T) {
	for in, want := range map[string]string{
		"/host_mnt/Users/a/web":      "/Users/a/web",
		"/run/host_mark/Users/a/web": "/Users/a/web",
		"/private/tmp/w":             "/tmp/w",
		"/home/a/web":                "/home/a/web",
	} {
		if got := hostPath(in); got != want {
			t.Errorf("hostPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLIPinned(t *testing.T) {
	for _, c := range []struct {
		d    config.Devcontainer
		want bool
	}{
		{config.Devcontainer{Command: "npx -y @devcontainers/cli@{version}", Version: "0.89.0"}, true},
		{config.Devcontainer{Command: "npx -y @devcontainers/cli@{version}"}, false},
		{config.Devcontainer{Command: "npx -y @devcontainers/cli@0.89.0"}, true},
		{config.Devcontainer{Command: "devcontainer", Version: "0.89.0"}, false},
	} {
		if got := CLIPinned(c.d); got != c.want {
			t.Errorf("CLIPinned(%+v) = %v", c.d, got)
		}
	}
}

func TestSeatPortsCoverEverySeatWithOffset(t *testing.T) {
	c := &config.Config{Projects: map[string]*config.Project{"web": {Servers: map[string]*config.Server{"app": {Port: 3000}}}},
		Resources: map[string]*config.Resource{"r": {Capacity: 1, Servers: []string{"web.app"}}}}
	c.Lanes.PortStride, c.Ports.Offset = 100, 10
	got := SeatPorts(c, "web")
	if len(got) != 2 || got[0] != 3010 || got[1] != 3110 {
		t.Fatalf("SeatPorts = %v", got)
	}
}

// A devcontainer's initializeCommand runs on the host, so in a rehearsal it needs the same names lane
// commands get, or a script branching on them acts on the owner's containers.
func TestComposeEnvInARehearsal(t *testing.T) {
	file := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(file, []byte("ports:\n  - \"${AW_PORT_5432:-5432}:5432\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ComposeEnv([]string{file}, 20000, "rhab")
	for _, want := range []string{"AW_PORT_5432=25432", "AW_REHEARSAL=1", "AW_NAMESPACE=rhab", "AW_INFRA_PROJECT=rhab-infra", "AW_INFRA_NETWORK=rhab-dev-net"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if got := ComposeEnv([]string{file}, 0, ""); slices.Contains(got, "AW_REHEARSAL=1") {
		t.Errorf("an ordinary run looks like a rehearsal: %v", got)
	}
}

// A hand-written infra file's own name is the stack's identity: under another project name its
// containers would start a second time, on empty volumes. A rehearsal never takes that name.
func TestInfraKeepsTheFilesName(t *testing.T) {
	owner := t.TempDir()
	if err := os.MkdirAll(filepath.Join(owner, "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(owner, "infra", "compose.yaml"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("# the stack\nname: rt-infra\nservices: {}\n")
	if got := NewInfra(owner, "ridetracker", nil, nil, 0).Project; got != "rt-infra" {
		t.Errorf("a named file: got project %s", got)
	}
	write("name: ${STACK:-rt}\nservices: {}\n")
	if got := NewInfra(owner, "ridetracker", nil, nil, 0).Project; got != "ridetracker-infra" {
		t.Errorf("a computed name: got project %s", got)
	}
	copied := filepath.Join(owner, ".worktrees", workspace.RehearsalPrefix+"ab")
	for _, d := range []string{"infra", ".aw"} {
		if err := os.MkdirAll(filepath.Join(copied, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for f, text := range map[string]string{"infra/compose.yaml": "name: rt-infra\n", workspace.RehearsalMarker: ""} {
		if err := os.WriteFile(filepath.Join(copied, filepath.FromSlash(f)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := NewInfra(copied, "rhab", nil, nil, 0).Project; got != "rhab-infra" {
		t.Errorf("a rehearsal: got project %s", got)
	}
}

// A generated slot is output, not an input: otherwise each start would force another rebuild.
func TestConfigHashInputs(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dev, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("devcontainer.json", `{ "dockerComposeFile": ["compose.yaml", "compose.local.yaml"], "service": "app" }`)
	write("compose.yaml", "services:\n  app:\n    image: ${AW_TEST_IMAGE}\n")
	c, err := config.LoadBytes(filepath.Join(dir, "workspace.toml"), []byte(`[workspace]
name = "Solo"
namespace = "solo"
schema = 1
[projects.solo]
directory = "."
runner = "devcontainer"
local_compose = "compose.local.yaml"
`))
	if err != nil {
		t.Fatal(err)
	}
	newHash := func() string {
		t.Helper()
		d, err := NewDevcontainer(c, dir, "solo", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d.hash
	}
	t.Setenv("AW_TEST_IMAGE", "first")
	first := newHash()
	write("compose.local.yaml", "old generated output")
	if got := newHash(); got != first {
		t.Fatalf("generated slot changed fingerprint: %s != %s", got, first)
	}
	t.Setenv("AW_TEST_IMAGE", "second")
	second := newHash()
	if second == first {
		t.Fatal("changed Compose environment was ignored")
	}
	write("compose.yaml", "services:\n  app:\n    image: different\n")
	if newHash() == second {
		t.Fatal("changed Compose input was ignored")
	}
	if err := os.Remove(filepath.Join(dev, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDevcontainer(c, dir, "solo", t.TempDir()); err == nil {
		t.Fatal("missing configuration must fail closed")
	}
}
