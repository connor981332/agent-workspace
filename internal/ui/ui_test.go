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

func TestCleanLines(t *testing.T) {
	in := "error: the branch 'aw/x' is not fully merged\r\nhint: If you are sure, run 'git branch -D aw/x'\nhint: Disable\x1b[31m this\n"
	want := "error: the branch 'aw/x' is not fully merged\n    hint: If you are sure, run 'git branch -D aw/x'\n    hint: Disable[31m this"
	if got := CleanLines(in, "    "); got != want {
		t.Errorf("CleanLines = %q, want %q", got, want)
	}
	if got := CleanLines("one line", "  "); got != "one line" {
		t.Errorf("CleanLines(one line) = %q", got)
	}
}

func TestBrief(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Short note.", 40, "Short note."},
		{"Simulator tests pass. Verify Apple sign-in by hand before merging.", 40, "Simulator tests pass. …"},
		{"Adds rounding for JPY; the EUR path is unchanged and tested", 40, "Adds rounding for JPY …"},
		{"one two three four five six seven eight nine ten", 20, "one two three four…"},
		{"averyveryverylongwordwithoutanyspaces", 10, "averyvery…"},
		{"  spread\n over\tlines  ", 40, "spread over lines"},
	}
	for _, c := range cases {
		if got := Brief(c.in, c.max); got != c.want {
			t.Errorf("Brief(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestFit(t *testing.T) {
	if got := Fit("abcdef", 4); got != "abc…" {
		t.Errorf("Fit = %q", got)
	}
	if got := Fit("ünï", 3); got != "ünï" {
		t.Errorf("Fit = %q", got)
	}
	if got := Fit("abc", 0); got != "abc" {
		t.Errorf("Fit = %q", got)
	}
}
