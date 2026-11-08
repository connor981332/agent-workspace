package cmds

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	var doc any
	dec := json.NewDecoder(strings.NewReader(`{"a":{"b":[{"c":"x"},2]},"n":0,"t":true,"z":null,"e":[]}`))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, want string
		found      bool
	}{
		{".a.b[0].c", "x", true},
		{".a.b[1]", "2", true},
		{".n", "0", true},
		{".t", "true", true},
		{".z", "null", true},
		{".e", "[]", true},
		{".a.b[2]", "", false},
		{".missing", "", false},
		{".a.b[0]", `{"c":"x"}`, true},
	}
	for _, c := range cases {
		got, found, err := lookup(doc, c.path)
		if err != nil {
			t.Errorf("%s: %v", c.path, err)
			continue
		}
		if found != c.found || (found && render(got) != c.want) {
			t.Errorf("%s = %q (found %v), want %q (found %v)", c.path, render(got), found, c.want, c.found)
		}
	}
	for _, bad := range []string{"a", ".a.b[x]", ".a.b[0", ".n.x", ".a[0]", ".."} {
		if _, _, err := lookup(doc, bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
