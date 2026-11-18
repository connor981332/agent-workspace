package inspect

import "testing"

func TestDropAccepted(t *testing.T) {
	p := &Project{Findings: []Finding{{File: ".env.template"}, {File: "ci/a/b.yml"}, {File: "src/app.ts"}}}
	n := p.DropAccepted(map[string]string{".env.template": "a template", "ci/**": "CI has its own services"})
	if n != 2 || len(p.Findings) != 1 || p.Findings[0].File != "src/app.ts" {
		t.Errorf("removed %d, left %v", n, p.Findings)
	}
}

// TestDropAcceptedGlobstar pins the keys from the 0.5.0 dogfood report (#111): a leading ** spans any
// number of directories, as the changelog promises, rather than one path segment.
func TestDropAcceptedGlobstar(t *testing.T) {
	files := []string{"README.md", "empower/.env.development", "evault/app/models/authentication_attempt.rb", "a/b/c/d/e/f/g.rb"}
	for _, key := range []string{"**", "**/*", "/**"} {
		p := &Project{}
		for _, f := range files {
			p.Findings = append(p.Findings, Finding{File: f})
		}
		if n := p.DropAccepted(map[string]string{key: "reviewed"}); n != len(files) || len(p.Findings) != 0 {
			t.Errorf("%q removed %d of %d, left %v", key, n, len(files), p.Findings)
		}
	}
	// A key without a slash matches a name at any depth, as in .gitignore; one with a slash does not.
	p := &Project{Findings: []Finding{{File: "config/.env"}, {File: "deep/config/settings.yml"}, {File: "config/settings.yml"}}}
	if n := p.DropAccepted(map[string]string{".env": "x", "config/settings.yml": "y"}); n != 2 || p.Findings[0].File != "deep/config/settings.yml" {
		t.Errorf("removed %d, left %v", n, p.Findings)
	}
}
