package providers

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// A rehearsal looks only in its own Compose project: the owner's database is a service of the same name.
func TestContainerQueryStaysInTheRehearsal(t *testing.T) {
	const own = "label=com.docker.compose.project=rhab-infra"
	if q := containerQuery("label=com.docker.compose.service=db", "rhab-infra"); !slices.Contains(q, own) {
		t.Errorf("a rehearsal's query is not limited to its project: %v", q)
	}
	if q := containerQuery("label=com.docker.compose.service=db", ""); slices.Contains(q, own) {
		t.Errorf("an ordinary query is limited: %v", q)
	}
}

func TestPostgresStarting(t *testing.T) {
	for msg, want := range map[string]bool{
		`psql: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed: FATAL: the database system is shutting down`: true,
		`psql: FATAL: the database system is starting up`:                                                            true,
		`psql: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed: No such file or directory`: true,
		`psql: FATAL: password authentication failed for user "postgres"`:                                            false,
		`psql: ERROR: database "x" does not exist`:                                                                   false,
	} {
		if got := postgresStarting(msg); got != want {
			t.Errorf("%s: got %v", msg, got)
		}
	}
}

// A custom provider has to find the copy's containers, and aw starts it, so aw must tell it the names.
func TestExternalProviderIsToldTheRehearsal(t *testing.T) {
	owner := filepath.Join(t.TempDir(), ".worktrees", "rehearsal-ab")
	marker := filepath.Join(owner, filepath.FromSlash(workspace.RehearsalMarker))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Workspace.Namespace = "rhab"
	cfg.Lanes.Data = map[string]*config.Data{"files": {Provider: "external", Command: `printf '{"ok":true,"env":{"P":"%s"}}' "$AW_INFRA_PROJECT" #`}}
	p, err := (&Set{Cfg: cfg, Owner: owner}).New("files")
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.Env(context.Background(), Lane{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if env["P"] != "rhab-infra" {
		t.Errorf("the provider was told %q", env["P"])
	}
}
