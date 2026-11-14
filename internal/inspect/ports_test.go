package inspect

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The ports aw writes into its own files, and the localhost URLs in a harness's permissions, are not
// the application's fixed ports; a port written into the application still is.
func TestHardcodedPortSkipsAWAndHarnessFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".devcontainer/aw.compose.yaml":      "# Written by aw; do not edit. It names this checkout's path, so it is not committed.\nservices:\n  app:\n    ports:\n      - \"127.0.0.1:3000:3000\"\n      - \"127.0.0.1:3100:3100\"\n",
		".devcontainer/aw/devcontainer.json": "// Written by aw; do not edit.\n{\"forwardPorts\": [\"localhost:3200\"]}\n",
		".claude/settings.local.json":        "{\"permissions\": {\"allow\": [\"Bash(curl http://localhost:3000/*)\", \"WebFetch(domain:localhost:3000)\"]}}\n",
		"opencode.json":                      "{\"permission\": {\"bash\": {\"curl localhost:3000\": \"allow\"}}}\n",
		"src/server.ts":                      "app.listen(3000)\nconst url = \"http://localhost:4000/api\"\n",
		"config/app.yml":                     "# Written by the team\nurl: http://127.0.0.1:5000\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Scan("next", dir, "next")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range p.Findings {
		if f.Kind == "hardcoded-port" {
			got[f.File] = true
		}
	}
	want := map[string]bool{"src/server.ts": true, "config/app.yml": true}
	if len(got) != len(want) || !got["src/server.ts"] || !got["config/app.yml"] {
		t.Errorf("hardcoded-port findings in %v, want exactly %v", got, want)
	}
}

func TestCommandTools(t *testing.T) {
	for cmd, want := range map[string][]string{
		"(bundle check || bundle install)": {"bundle", "bundle"},
		"npm ci":                           {"npm"},
		"cd api && RAILS_ENV=test bin/rails db:prepare": nil,
		"exec yarn install --frozen-lockfile":           {"yarn"},
		"{ pnpm install; } | tee log":                   {"pnpm", "tee"},
		"if [ -f Gemfile ]; then bundle install; fi":    {"bundle"},
		"$HOME/bin/setup":                               nil,
	} {
		if got := commandTools(cmd); !slices.Equal(got, want) {
			t.Errorf("commandTools(%q) = %q, want %q", cmd, got, want)
		}
	}
}
