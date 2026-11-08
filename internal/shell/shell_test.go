package shell

import (
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
