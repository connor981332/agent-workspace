package inspect

import "testing"

func TestDropAccepted(t *testing.T) {
	p := &Project{Findings: []Finding{{File: ".env.template"}, {File: "ci/a/b.yml"}, {File: "src/app.ts"}}}
	n := p.DropAccepted(map[string]string{".env.template": "a template", "ci/**": "CI has its own services"})
	if n != 2 || len(p.Findings) != 1 || p.Findings[0].File != "src/app.ts" {
		t.Errorf("removed %d, left %v", n, p.Findings)
	}
}
