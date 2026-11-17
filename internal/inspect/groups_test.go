package inspect

import (
	"slices"
	"testing"
)

func TestGroupFiles(t *testing.T) {
	var entries []string
	for _, g := range []string{"a", "b", "c", "d"} {
		entries = append(entries, "gems/"+g+"/rspec.xml", "gems/"+g+"/.rspec_status")
	}
	for _, n := range []string{"1", "2", "3"} {
		entries = append(entries, "docs/notes-"+n+".md")
	}
	entries = append(entries, ".env.bak", "log/development.log", ".devcontainer/.env")
	groups := groupFiles(entries)
	want := []FileGroup{
		{"**/.rspec_status", 4, "test output"}, {"**/rspec.xml", 4, "test output"}, {"docs/*.md", 3, ""},
		{".devcontainer/.env", 1, ""}, {".env.bak", 1, "editor backup"}, {"log/development.log", 1, "log"},
	}
	if !slices.Equal(groups, want) {
		t.Fatalf("groupFiles:\n got %v\nwant %v", groups, want)
	}
	if got, want := suggestGenerated(groups), []string{"**/.rspec_status", "**/rspec.xml", "*.log"}; !slices.Equal(got, want) {
		t.Errorf("suggestGenerated = %q, want %q", got, want)
	}
}
