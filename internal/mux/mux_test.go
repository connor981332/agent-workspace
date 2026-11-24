package mux

import "testing"

func TestFindLabel(t *testing.T) {
	for _, c := range []struct{ reply, want string }{
		{`{"labels":{"herdr.space":"s3"}}`, "s3"},
		{`{"result":{"terminal":{"labels":{"herdr.tab":"t1","herdr.space":"s9"}}}}`, "s9"},
		{`[{"labels":{"herdr.space":""}},{"labels":{"herdr.space":"s2"}}]`, "s2"},
		{`{"labels":{}}`, ""},
		{`not json`, ""},
	} {
		if got := findLabel([]byte(c.reply), "herdr.space"); got != c.want {
			t.Errorf("findLabel(%s) = %q, want %q", c.reply, got, c.want)
		}
	}
}
