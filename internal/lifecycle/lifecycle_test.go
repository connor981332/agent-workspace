package lifecycle

import (
	"slices"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
)

func TestPlan(t *testing.T) {
	c := &config.Config{Projects: map[string]*config.Project{
		"web":    {Install: "npm ci", Lifecycle: config.ProjectLifecycle{Prepare: &config.Hook{Run: "p"}, Setup: &config.Hook{Run: "s"}, Reset: &config.Hook{Run: "r"}}},
		"shared": {Install: "npm ci", Lanes: "shared", Lifecycle: config.ProjectLifecycle{Setup: &config.Hook{Run: "s"}}},
	}}
	c.Lifecycle.Prepare = &config.Hook{Run: "p"}
	want := []string{"prepare", "web.prepare", "shared.setup", "web.install", "web.setup"}
	if got := Names(Plan(c, false, nil)); !slices.Equal(got, want) {
		t.Fatalf("new lane: got %v, want %v", got, want)
	}
	want = []string{"prepare", "web.prepare", "web.install", "web.reset"}
	if got := Names(Plan(c, true, nil)); !slices.Equal(got, want) {
		t.Fatalf("recycled lane: got %v, want %v", got, want)
	}
}

func TestNeeded(t *testing.T) {
	want := Needed("web", []string{"api.setup"})
	for step, ok := range map[string]bool{
		"prepare": true, "data.db": true, "web.install": true, "web.setup": true, "api.setup": true,
		"api.install": false, "webby.setup": false,
	} {
		if want(step) != ok {
			t.Errorf("%s: got %v, want %v", step, want(step), ok)
		}
	}
}
