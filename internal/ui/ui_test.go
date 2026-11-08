package ui

import "testing"

func TestClean(t *testing.T) {
	cases := map[string]string{
		"plain":                  "plain",
		"red\x1b[31mtext\x1b[0m": "red[31mtext[0m",
		"bell\a and back\b":      "bell and back",
		"tab\there":              "tab here",
		"c1\u009bcsi":            "c1csi",
		"line\nbreak\r":          "linebreak",
		"ünïcødé ✓":              "ünïcødé ✓",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
