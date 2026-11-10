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
