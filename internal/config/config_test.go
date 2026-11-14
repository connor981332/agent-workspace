package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const minimal = `[workspace]
name = "Shop"
namespace = "shop"
schema = 1

[projects.web]
`

func loadString(t *testing.T, team string) (*Config, error) {
	t.Helper()
	return LoadBytes("/ws/workspace.toml", []byte(team))
}

func mustLoad(t *testing.T, team string) *Config {
	t.Helper()
	c, err := loadString(t, team)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestDefaults(t *testing.T) {
	c := mustLoad(t, minimal)
	if c.Workspace.Forge != "auto" || c.Lanes.Max != 10 || c.Lanes.Pool != 3 || c.Lanes.PortStride != 100 ||
		c.Lanes.ServerCapacity != 2 || time.Duration(c.Lanes.Retention) != 14*24*time.Hour || c.Lanes.Hostname != "lane-{lane}.localhost" {
		t.Errorf("unexpected defaults: %+v %+v", c.Workspace, c.Lanes)
	}
	web := c.Projects["web"]
	if web.Directory != "web" || web.Runner != "host" || web.Lanes != "worktree" {
		t.Errorf("project defaults: %+v", web)
	}
	if c.User.Harness != "claude" || c.User.OwnerSessions != "normal" {
		t.Errorf("user defaults: %+v", c.User)
	}
	got, ok := c.Get(Path{"projects", "web", "runner"})
	if !ok || got != "host" {
		t.Errorf("Get projects.web.runner = %v, %v", got, ok)
	}
	prov, _ := c.Explain(Path{"projects", "web", "runner"})
	if len(prov) != 1 || prov[0].Settings[0].Layer != LayerDefault {
		t.Errorf("provenance: %+v", prov)
	}
}

// The C1 cases (test_mistakes_are_errors_rather_than_ignored), adapted to this schema, plus the
// spec's own: each is an error naming the key, the line and a fix.
func TestMistakesAreErrors(t *testing.T) {
	cases := []struct {
		name, toml, want string
		line             int
	}{
		{"no projects", "[workspace]\nname = \"S\"\nnamespace = \"s\"\nschema = 1\n", "has no projects", 0},
		{"unknown table", minimal + "[typo]\n", "typo: unknown key", 7},
		{"unknown key", minimal + "[lanes]\nslot = 2\n", "lanes.slot: unknown key", 8},
		{"suggestion", minimal + "[lanes]\nmaxx = 2\n", "did you mean max?", 8},
		{"bad namespace", strings.Replace(minimal, `"shop"`, `"Bad-Name"`, 1), "workspace.namespace: is not a valid namespace", 3},
		{"bad forge", minimal + "[workspace]\n", "", 0},
		{"forge", strings.Replace(minimal, "schema = 1", "schema = 1\nforge = \"bitbucket\"", 1), `workspace.forge: must be one of auto, github, gitlab, none`, 5},
		{"reserved all", minimal + "[projects.all]\ndirectory = \"x\"\n", `"all" is reserved`, 7},
		{"escaping directory", minimal + "[projects.a]\ndirectory = \"../x\"\n", "leaves the workspace", 8},
		{"absolute directory", minimal + "[projects.a]\ndirectory = \"/x\"\n", "must be relative", 8},
		{"dot not alone", minimal + "[projects.a]\ndirectory = \".\"\n", "must be the only project", 8},
		{"duplicate directory", minimal + "[projects.a]\ndirectory = \"web\"\n", "directories must be unique", 6},
		{"bad runner", minimal + "runner = \"podman\"\n", "projects.web.runner: must be one of host, devcontainer", 7},
		{"server without port", minimal + "[projects.web.servers.next]\ncommand = \"x\"\n", "has no base port", 7},
		{"after unknown", minimal + "[projects.web.servers.next]\ncommand = \"x\"\nport = 1\nafter = [\"api\"]\n", `there is no server named "api"`, 10},
		{"needs unknown", minimal + "[commands.e2e]\nproject = \"web\"\nrun = \"x\"\nneeds = [\"stack\"]\n", `there is no resource named "stack"`, 10},
		{"string for int", minimal + "[lanes]\nmax = \"3\"\n", "lanes.max: must be an integer, not a string", 8},
		{"bad duration", minimal + "[lanes]\nretention = \"2 weeks\"\n", "is not a duration", 8},
		{"hostname without lane", minimal + "[lanes]\nhostname = \"app.localhost\"\n", "does not contain {lane}", 8},
		{"pool over max", minimal + "[lanes]\nmax = 2\npool = 3\n", "more than lanes.max", 9},
		{"schema missing", strings.Replace(minimal, "schema = 1\n", "", 1), "workspace.schema: is missing", 0},
		{"schema newer", strings.Replace(minimal, "schema = 1", "schema = 9", 1), "but this aw reads schema 1", 4},
		{"syntax", minimal + "[lanes\n", "invalid TOML", 7},
		{"process without capacity", minimal + "[resources.stack]\nkind = \"process\"\n", "has no capacity", 7},
		{"token mismatch", minimal + "[resources.android]\nkind = \"token\"\ntokens = [\"a\"]\ncapacity = 2\n", "but 1 tokens are listed", 10},
		{"server in two resources", minimal + "[projects.web.servers.next]\ncommand = \"x\"\nport = 1\n[resources.a]\nkind = \"process\"\ncapacity = 1\nservers = [\"web.next\"]\n[resources.b]\nkind = \"process\"\ncapacity = 1\nservers = [\"web.next\"]\n", "already belongs to resource a", 17},
		{"hook without run", minimal + "[projects.web.lifecycle]\nsetup = { timeout = \"1m\" }\n", "has no run key", 8},
		{"hook wrong type", minimal + "[lifecycle]\nprepare = 3\n", "must be a command string or a table", 8},
		{"bad lock", minimal + "[locks]\nkeys = [\"nonsense.key\"]\n", "matches no configuration key", 8},
		{"command unknown project", minimal + "[commands.e2e]\nproject = \"mobile\"\nrun = \"x\"\n", `there is no project named "mobile"`, 8},
		{"bad project name", minimal + "[projects.Web]\n", `"Web" is not a valid name`, 7},
		{"bad audited_with", strings.Replace(minimal, "schema = 1", "schema = 1\naudited_with = \"v0.2\"", 1), "workspace.audited_with: is not a version", 5},
		{"bad declined id", minimal + "[audit.declined]\n\"Bad Id\" = \"x\"\n", `use the suggestion's id`, 8},
		{"step", minimal + "[commands.e2e]\nproject = \"web\"\nrun = \"x\"\nafter = [\"web.deploy\"]\n", "is not a lifecycle step", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, tc.toml)
			if tc.want == "" {
				return // the case only checks that loading does not panic
			}
			var errs Errors
			if !errors.As(err, &errs) {
				t.Fatalf("got %v, want a configuration error containing %q", err, tc.want)
			}
			var found *Error
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					found = e
				}
			}
			if found == nil {
				t.Fatalf("no error contains %q:\n%v", tc.want, err)
			}
			if found.Fix == "" {
				t.Errorf("error has no fix: %v", found)
			}
			if found.Pos.File != "/ws/workspace.toml" {
				t.Errorf("error names file %q", found.Pos.File)
			}
			if tc.line != 0 && found.Pos.Line != tc.line {
				t.Errorf("error is at line %d, want %d: %v", found.Pos.Line, tc.line, found)
			}
		})
	}
}

func TestServerCycle(t *testing.T) {
	_, err := loadString(t, minimal+`
[projects.web.servers.a]
command = "x"
port = 1
after = ["b"]

[projects.web.servers.b]
command = "x"
port = 2
after = ["api.c"]

[projects.api.servers.c]
command = "x"
port = 3
after = ["web.a"]
`)
	if err == nil || !strings.Contains(err.Error(), "cycle: api.c → web.a → web.b → api.c") {
		t.Fatalf("got %v", err)
	}
}

func TestLayersLocksAndProvenance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	owner := filepath.Join(dir, "ws")
	if err := os.MkdirAll(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	team := minimal + `
[lanes]
max = 6

[resources.stack]
kind = "process"
capacity = 1
owner_preempts = true

[locks]
keys = ["resources.*.owner_preempts"]
`
	if err := os.WriteFile(filepath.Join(owner, TeamFile), []byte(team), 0o644); err != nil {
		t.Fatal(err)
	}
	indiv := IndividualFile("shop")
	if err := os.MkdirAll(filepath.Dir(indiv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indiv, []byte("[lanes]\nmax = 4\n\n[resources.stack]\nowner_preempts = false\n\n[user]\nharness = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(owner)
	if err != nil {
		t.Fatal(err)
	}
	if c.Lanes.Max != 4 || c.User.Harness != "codex" {
		t.Errorf("individual layer not applied: max %d, harness %q", c.Lanes.Max, c.User.Harness)
	}
	if !c.Resources["stack"].OwnerPreempts {
		t.Errorf("a locked key was overridden")
	}
	if len(c.Refused) != 1 || c.Refused[0].Key != "resources.stack.owner_preempts" || c.Refused[0].Pos.Line != 5 {
		t.Errorf("refusal not reported: %+v", c.Refused)
	}
	prov, _ := c.Explain(Path{"lanes", "max"})
	if len(prov) != 1 {
		t.Fatalf("explain: %+v", prov)
	}
	var layers []string
	for _, s := range prov[0].Settings {
		layers = append(layers, s.Layer+"="+s.Status)
	}
	if strings.Join(layers, " ") != "default=overridden team=overridden individual=effective" {
		t.Errorf("lanes.max settings: %v", layers)
	}
	if s := prov[0].Settings[1]; s.Pos.File != filepath.Join(owner, TeamFile) || s.Pos.Line != 9 {
		t.Errorf("team setting position: %+v", s.Pos)
	}

	// Team-only keys are refused in the individual file.
	if err := os.WriteFile(indiv, []byte("[workspace]\nnamespace = \"other\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(owner)
	if err == nil || !strings.Contains(err.Error(), "can only be set in the team layer") || !strings.Contains(err.Error(), indiv+":2:1") {
		t.Errorf("team-only key in the individual layer: %v", err)
	}
}

func TestCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	owner := filepath.Join(dir, "ws")
	if err := os.MkdirAll(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	team := filepath.Join(owner, TeamFile)
	if err := os.WriteFile(team, []byte(minimal+"[lanes]\nmax = 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(owner)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "config.cache")
	if err := WriteCache(cache, "v1", c); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCache(cache, "v1")
	if err != nil {
		t.Fatalf("fresh cache: %v", err)
	}
	if got.Lanes.Max != 5 || got.Projects["web"].Runner != "host" {
		t.Errorf("cached config differs: %+v", got.Lanes)
	}
	if prov, _ := got.Explain(Path{"lanes", "max"}); len(prov) != 1 || prov[0].Settings[1].Pos.Line != 8 {
		t.Errorf("cached provenance: %+v", prov)
	}
	if _, err := ReadCache(cache, "v2"); err != ErrStale {
		t.Errorf("another version's cache was used")
	}
	// An individual file appearing makes the cache stale.
	indiv := IndividualFile("shop")
	if err := os.MkdirAll(filepath.Dir(indiv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indiv, []byte("[lanes]\nmax = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCache(cache, "v1"); err != ErrStale {
		t.Errorf("cache not stale after the individual file appeared")
	}
	if err := os.Remove(indiv); err != nil {
		t.Fatal(err)
	}
	// So does a change to the team file.
	if err := os.WriteFile(team, []byte(minimal+"[lanes]\nmax = 6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCache(cache, "v1"); err != ErrStale {
		t.Errorf("cache not stale after the team file changed")
	}
	if err := os.WriteFile(cache, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCache(cache, "v1"); err != ErrStale {
		t.Errorf("a corrupt cache was used")
	}
}

func TestPaths(t *testing.T) {
	for _, s := range []string{"lanes.max", `projects."my app".runner`, "locks.keys[0]", "a.b[1][2].c"} {
		p, err := ParsePath(s)
		if err != nil {
			t.Errorf("%s: %v", s, err)
			continue
		}
		if p.String() != s {
			t.Errorf("round trip %s → %s", s, p)
		}
	}
	for _, bad := range []string{"", "a..b", "a.", `a."b`, "a[x]", "a b"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{"15s": 15 * time.Second, "45m": 45 * time.Minute, "14d": 14 * 24 * time.Hour, "1h30m": 90 * time.Minute, "0s": 0}
	for s, want := range cases {
		if got, err := ParseDuration(s); err != nil || got != want {
			t.Errorf("%s = %v, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "d", "1.5d", "-1s", "-1d", "2 weeks", "1dd"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A key after a [table] belongs to that table in TOML, which reads as a strange error about a key
// nobody meant to put there, so the error says how it happened.
func TestKeyAfterAStringTableSaysToMoveIt(t *testing.T) {
	_, err := LoadBytes("/ws/workspace.toml", []byte(`[workspace]
name = "a"
namespace = "a"
schema = 1

[projects.web.accepted_findings]
"ci/**" = "reviewed"
generated = ["node_modules"]
`))
	if err == nil || !strings.Contains(err.Error(), "move it before [projects.web.accepted_findings]") {
		t.Fatalf("got: %v", err)
	}
}

func TestLocalCompose(t *testing.T) {
	for _, tc := range []struct{ runner, path, want string }{
		{"host", "compose.local.yaml", "requires runner"},
		{"devcontainer", "/tmp/compose.yaml", "not a clean path"},
		{"devcontainer", "../compose.yaml", "not a clean path"},
		{"devcontainer", "nested/../compose.yaml", "not a clean path"},
		{"devcontainer", ".", "not a clean path"},
		{"devcontainer", "compose.local.yaml", ""},
		{"devcontainer", "local/compose.yaml", ""},
	} {
		t.Run(tc.runner+"/"+tc.path, func(t *testing.T) {
			_, err := loadString(t, minimal+"runner = \""+tc.runner+"\"\nlocal_compose = \""+tc.path+"\"\n")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "projects.web.local_compose:") || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "→") {
				t.Fatalf("got %v; want local_compose error with %q and a fix", err, tc.want)
			}
		})
	}
}

func TestAuditKeys(t *testing.T) {
	c := mustLoad(t, strings.Replace(minimal, "schema = 1", "schema = 1\naudited_with = \"0.2.1\"", 1)+
		"[audit.declined]\ndevcontainer-local-slot = \"we keep one configuration\"\n")
	if c.Workspace.AuditedWith != "0.2.1" || c.Audit.Declined["devcontainer-local-slot"] != "we keep one configuration" {
		t.Fatalf("got audited_with %q, declined %v", c.Workspace.AuditedWith, c.Audit.Declined)
	}
	for _, k := range []string{"workspace.audited_with", "audit.declined", "projects.<name>.local_compose", "lanes.max"} {
		if !KnownKey(k) {
			t.Errorf("KnownKey(%q) = false", k)
		}
	}
	for _, k := range []string{"aw.pull", "projects.<name>.nonsense", "devcontainer.json", "", "a..b"} {
		if KnownKey(k) {
			t.Errorf("KnownKey(%q) = true", k)
		}
	}
}
