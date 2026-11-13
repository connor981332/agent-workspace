package broker

import (
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
)

func TestDevcontainerOwnerPortKeepsPublicationOffset(t *testing.T) {
	cfg := &config.Config{Projects: map[string]*config.Project{
		"web": {Runner: "devcontainer", Servers: map[string]*config.Server{
			"app": {Port: 3000, OwnerPort: 3000},
		}},
	}}
	cfg.Ports.Offset = 1000
	calls := 0
	b := &Broker{Cfg: cfg, ListeningIn: func(project string, port int) bool {
		calls++
		if project != "web" || port != 4000 {
			t.Fatalf("probing %s:%d; want web:4000", project, port)
		}
		return true
	}}
	if !b.ownerPortBusy(Resource{OwnerCounts: true, Servers: []string{"web.app"}}) || calls != 1 {
		t.Fatal("the owner's container publication must count as a seat")
	}
}
