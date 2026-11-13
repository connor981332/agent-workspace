package audit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connor981332/agent-workspace/internal/config"
)

const team = `[workspace]
name = "Shop"
namespace = "shop"
schema = 1
%s
[projects.api]
directory = "api"
runner = "devcontainer"
`

func load(t *testing.T, stamp, extra string) *config.Config {
	t.Helper()
	text := strings.Replace(team, "%s", stamp, 1) + extra
	c, err := config.LoadBytes("/ws/workspace.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// project makes owner/api a Git repository whose devcontainer.json lists files, with ignored in
// its .gitignore.
func project(t *testing.T, files, ignored string) string {
	t.Helper()
	owner := t.TempDir()
	dir := filepath.Join(owner, "api")
	must(t, os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".devcontainer", "devcontainer.json"),
		[]byte(`{"dockerComposeFile": `+files+`, "service": "api"}`), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignored+"\n"), 0o644))
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return owner
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ids(s []Suggestion) []string {
	out := []string{}
	for _, x := range s {
		out = append(out, x.ID)
	}
	return out
}

func TestStamp(t *testing.T) {
	ctx := context.Background()
	owner := t.TempDir() // api has no devcontainer here, so only the stamp speaks
	cases := []struct {
		stamp, version string
		want           []string
	}{
		{"", "0.2.1", []string{RuleAuditedWithMissing}},
		{`audited_with = "0.1.5"`, "0.2.1", []string{RuleAuditedWithOld}},
		{`audited_with = "0.1.0-rc.2"`, "0.2.1", []string{RuleAuditedWithOld}},
		{`audited_with = "0.2.0"`, "0.2.1", []string{}},
		{`audited_with = "0.1.5"`, "dev", []string{}},
		{`audited_with = "0.1.5"`, "0.3.0-rc.1", []string{}},
	}
	for _, tc := range cases {
		got := ids(Suggest(ctx, owner, load(t, tc.stamp, ""), tc.version))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q on %s: got %v, want %v", tc.stamp, tc.version, got, tc.want)
		}
	}
}

func TestDevcontainerSuggestions(t *testing.T) {
	ctx := context.Background()
	stamp := `audited_with = "0.2.1"`
	slot := project(t, `["compose.yaml", "compose.local.yaml"]`, ".devcontainer/compose.local.yaml")
	if got := ids(Suggest(ctx, slot, load(t, stamp, ""), "0.2.1")); !slices.Equal(got, []string{"devcontainer-local-slot-api"}) {
		t.Errorf("a local slot: got %v", got)
	}
	named := load(t, stamp, "local_compose = \"compose.local.yaml\"\n")
	if got := ids(Suggest(ctx, slot, named, "0.2.1")); len(got) != 0 {
		t.Errorf("a slot local_compose names: got %v", got)
	}
	wired := project(t, `["compose.yaml", "aw.compose.yaml"]`, "")
	if got := ids(Suggest(ctx, wired, load(t, stamp, ""), "0.2.1")); !slices.Equal(got, []string{"devcontainer-committed-wiring-api"}) {
		t.Errorf("committed wiring: got %v", got)
	}
	plain := project(t, `["compose.yaml"]`, "")
	if got := ids(Suggest(ctx, plain, load(t, stamp, ""), "0.2.1")); len(got) != 0 {
		t.Errorf("an unwired project, which aw already generates for: got %v", got)
	}
}

func TestDeclined(t *testing.T) {
	ctx := context.Background()
	owner := project(t, `["compose.yaml", "compose.local.yaml"]`, ".devcontainer/compose.local.yaml")
	decline := "[audit.declined]\ndevcontainer-local-slot-api = \"we keep one configuration\"\n"
	if got := ids(Suggest(ctx, owner, load(t, `audited_with = "0.2.1"`, decline), "0.2.1")); len(got) != 0 {
		t.Errorf("declined after the rule existed: got %v", got)
	}
	// Declined in an audit before aw offered the alternative: the rule is newer, so it comes back.
	if got := ids(Suggest(ctx, owner, load(t, `audited_with = "0.1.5"`, decline), "0.1.9")); !slices.Equal(got, []string{"devcontainer-local-slot-api"}) {
		t.Errorf("declined before the rule existed: got %v", got)
	}
}

func TestKeyIn(t *testing.T) {
	cases := map[string]string{
		"projects.<name>.env":                  "projects.<name>.env",
		"[projects.<name>.env]":                "projects.<name>.env",
		"[workspace] generated":                "workspace.generated",
		`local_compose = "compose.local.yaml"`: "projects.<name>.local_compose",
		`locks.keys = ["choose.<name>"]`:       "locks.keys",
		"aw doctor":                            "",
		"workspace.toml":                       "",
		"for":                                  "",
		"[options.<name>]":                     "options.<name>",
	}
	for span, want := range cases {
		if got := keyIn(span); got != want {
			t.Errorf("keyIn(%q) = %q, want %q", span, got, want)
		}
	}
}

func TestTouching(t *testing.T) {
	c := load(t, "", "local_compose = \"compose.local.yaml\"\n")
	changes := []Change{
		{Keys: []string{"projects.<name>.local_compose"}},
		{Keys: []string{"lanes.max"}},
		{Keys: []string{"projects.<name>"}},
		{},
	}
	got := Touching(c, changes)
	if len(got) != 2 || got[0].Keys[0] != "projects.<name>.local_compose" || got[1].Keys[0] != "projects.<name>" {
		t.Fatalf("got %+v", got)
	}
}
