package forge

import (
	"testing"
	"time"
)

func TestParseRemote(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:o/r.git":                   {"github.com", "o/r"},
		"git@github.com:o/r":                       {"github.com", "o/r"},
		"https://github.com/o/r.git":               {"github.com", "o/r"},
		"ssh://git@gitlab.example.com:2222/g/s/r":  {"gitlab.example.com", "g/s/r"},
		"https://user@GitLab.example.com/g/r.git/": {"gitlab.example.com", "g/r"},
	}
	for in, want := range cases {
		host, project, ok := ParseRemote(in)
		if !ok || host != want[0] || project != want[1] {
			t.Errorf("ParseRemote(%q) = %q, %q, %v", in, host, project, ok)
		}
	}
	for _, in := range []string{"/srv/git/r.git", "../r.git", "file:///srv/r.git", "github.com", ""} {
		if _, _, ok := ParseRemote(in); ok {
			t.Errorf("ParseRemote(%q) has a forge", in)
		}
	}
}

// C1 test_generic.py::ForgeTests::test_adapter_choice.
func TestAdapterChoice(t *testing.T) {
	d := time.Now().Add(time.Minute)
	kind := func(setting, host string) string {
		if c := For(setting, host, d); c != nil {
			return c.Kind()
		}
		return ""
	}
	cases := []struct{ setting, host, want string }{
		{"auto", "github.com", "github"},
		{"auto", "gitlab.example.com", "gitlab"},
		{"auto", "code.example.com", ""},
		{"none", "github.com", ""},
		{"github", "code.example.com", "github"},
		{"gitlab", "github.com", "gitlab"},
	}
	for _, c := range cases {
		if got := kind(c.setting, c.host); got != c.want {
			t.Errorf("For(%q, %q) = %q, want %q", c.setting, c.host, got, c.want)
		}
	}
}
