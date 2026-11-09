package runners

import (
	"encoding/json"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
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
