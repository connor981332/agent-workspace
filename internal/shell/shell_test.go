package shell

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	start := Dir{Logical: "/w/lane", Physical: "/w/lane", Known: true}
	for _, tc := range []struct {
		src   string
		names []string // every simple command, in order
		last  Dir      // the directory of the last command
	}{
		{"ls", []string{"ls"}, start},
		{"cd /tmp && ls", []string{"cd", "ls"}, Dir{"/tmp", Resolve("/tmp"), true}},
		{"(cd /tmp); ls", []string{"cd", "ls"}, start},
		{"cd /tmp | ls", []string{"cd", "ls"}, start},
		{"false || cd /tmp; ls", []string{"false", "cd", "ls"}, Dir{"/w/lane", "/w/lane", false}},
		{"echo $(git status)", []string{"git", "echo"}, start},
		{"sudo -u root timeout 5 env A=1 rm x", []string{"sudo", "timeout", "env", "rm"}, start},
		{"bash -c 'cd /tmp && ls'", []string{"bash", "cd", "ls"}, Dir{"/tmp", Resolve("/tmp"), true}},
		{"xargs -n1 kill < pids", []string{"xargs", "kill"}, start},
		{"command -v git", []string{"command"}, start},
	} {
		p, err := Parse(tc.src, start, &Env{Home: "/home/u"})
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		var names []string
		for _, c := range p.Commands {
			names = append(names, c.Name())
		}
		if !slices.Equal(names, tc.names) {
			t.Errorf("%s: commands %q, want %q", tc.src, names, tc.names)
		}
		if got := p.Commands[len(p.Commands)-1].Dir; got != tc.last {
			t.Errorf("%s: last directory %+v, want %+v", tc.src, got, tc.last)
		}
	}
}

func TestWords(t *testing.T) {
	start := Dir{Logical: "/w/lane", Physical: "/w/lane", Known: true}
	for src, want := range map[string]Word{
		`x`:             {"x", true},
		`'a b'`:         {"a b", true},
		`it\'s`:         {"it's", true},
		`"a\"b\x"`:      {`a"b\x`, true},
		`~/notes`:       {"/home/u/notes", true},
		`~bob/x`:        {"~bob/x", false},
		`$HOME/x`:       {"/home/u/x", true},
		`"${HOME}"/x`:   {"/home/u/x", true},
		`$PWD/x`:        {"/w/lane/x", true},
		`$(pwd)/x`:      {"/w/lane/x", true},
		`$X/x`:          {"/x", false},
		`$(git root)/x`: {"/x", false},
		`${HOME:-/tmp}`: {"", false},
		`$((1+2))`:      {"", false},
		`a"$(pwd)"b`:    {"a/w/laneb", true},
		`$'\x41'`:       {`\x41`, false},
	} {
		p, err := Parse("echo "+src, start, &Env{Home: "/home/u"})
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		c := p.Commands[len(p.Commands)-1]
		if got := c.Args[1]; got != want {
			t.Errorf("%s: %+v, want %+v", src, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"echo 'open", "bash -c \"$X\"", "eval $CMD", "if then fi"} {
		if _, err := Parse(src, Dir{Logical: "/", Physical: "/", Known: true}, &Env{}); err == nil {
			t.Errorf("%s: no error", src)
		}
	}
}

// TestKnownValues covers values the command itself makes known: variables assigned earlier, for
// loops over literal words and globs, and functions defined and called in it. Each case names a
// command and the arguments (or redirect targets) aw should see for it, every time it runs.
func TestKnownValues(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"a/.worktrees/01", "b/.worktrees/01"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	start := Dir{Logical: "/w/lane", Physical: "/w/lane", Known: true}
	for _, tc := range []struct {
		src  string
		name string
		want []Word // the last argument of each run of the command named name
	}{
		{"A=/abs/path; grep -rn X $A/docs", "grep", []Word{{"/abs/path/docs", true}}},
		{`A=/abs; B="$A/x"; cat "$B"`, "cat", []Word{{"/abs/x", true}}},
		{"export A=/abs; cat $A", "cat", []Word{{"/abs", true}}},
		{"for r in a b; do (cd \"./$r\" && git status); done", "cd", []Word{{"./a", true}, {"./b", true}}},
		{"for d in " + dir + "/*/.worktrees/01; do git -C \"$d\" status; done", "git", []Word{{"status", true}, {"status", true}}},
		{"chk() { git -C \"$2\" status; }; chk a /abs/lane/a", "git", []Word{{"status", true}}},
		{`P=group%2Fproject; glab api "projects/$P/pipeline_schedules/123"`, "glab", []Word{{"projects/group%2Fproject/pipeline_schedules/123", true}}},
		// Unknown: from the environment, split or globbed when unquoted, changed in a branch, a loop
		// or the right of || or &&, set by a builtin, or local to a function.
		{"cat $UNSET/x", "cat", []Word{{"/x", false}}},
		{"A='a b'; cat $A", "cat", []Word{{"a b", false}}},
		{"A='a b'; cat \"$A\"", "cat", []Word{{"a b", true}}},
		{"A=/l; if x; then A=/o; fi; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; false || A=/o; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; true && A=/o; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; true && A=/o && cat $A", "cat", []Word{{"/o", true}}},
		{"A=/l; read A; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; for x in 1 2; do cat $A; A=/o; done", "cat", []Word{{"", false}, {"", false}}},
		{"A=/l; while x; do cat $A; A=/o; done", "cat", []Word{{"", false}}},
		{"A=/l; (A=/o); cat $A", "cat", []Word{{"/l", true}}},
		{"A=/l; f() { local A=/o; }; f; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; source x; cat $A", "cat", []Word{{"", false}}},
		{"A=/l; : ${A:=/o}; cat $A", "cat", []Word{{"", false}}},
		{"A=/l cat $A", "cat", []Word{{"", false}}},
		{"cat /lane/{a,../../owner}", "cat", []Word{{"/lane/{a,../../owner}", false}}},
		{"f() { cat $1; }", "cat", []Word{{"", false}}}, // never called: walked with nothing known
	} {
		p, err := Parse(tc.src, start, &Env{Home: "/home/u"})
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		var got []Word
		for _, c := range p.Commands {
			if c.Name() == tc.name {
				got = append(got, c.Args[len(c.Args)-1])
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %s gets %+v, want %+v", tc.src, tc.name, got, tc.want)
		}
	}
}

func TestKnownDirectories(t *testing.T) {
	start := Dir{Logical: "/w/lane", Physical: "/w/lane", Known: true}
	for _, tc := range []struct {
		src  string
		last Dir
	}{
		{"cd /w/lane/scratch && (test -d repo || git clone u repo) && cd repo && git log -1", Dir{"/w/lane/scratch/repo", "/w/lane/scratch/repo", true}},
		{"test -d x || mkdir x; ls", start},
		{"cd /tmp || true; ls", Dir{"/tmp", Resolve("/tmp"), false}},
		{"S=/w/lane/s; cd $S && ls", Dir{"/w/lane/s", "/w/lane/s", true}},
		{"go() { cd /tmp; }; go; ls", Dir{"/tmp", Resolve("/tmp"), false}},
	} {
		p, err := Parse(tc.src, start, &Env{Home: "/home/u"})
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got := p.Commands[len(p.Commands)-1].Dir; got != tc.last {
			t.Errorf("%s: last directory %+v, want %+v", tc.src, got, tc.last)
		}
	}
}

func TestRecursiveFunctionIsAnError(t *testing.T) {
	if _, err := Parse("f() { f; }; f", Dir{Logical: "/", Physical: "/", Known: true}, &Env{}); err == nil {
		t.Error("no error")
	}
}
