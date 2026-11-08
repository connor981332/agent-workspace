// Package shell parses a shell command into the simple commands it would run, for guard decisions
// (architecture.md, "Policy engine", step 3). It never runs anything: words whose value depends on
// an expansion are marked unknown, and callers fail closed on them.
//
// This package is on the hot path: it may not import os/exec or net.
package shell

import (
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A word is one shell word as far as it can be known without running anything. Known is false
// when it contains an expansion whose value cannot be known (a variable, a command substitution
// other than $(pwd), arithmetic). Text then holds the literal parts, so a caller can still see
// that the word looks like a path.
type Word struct {
	Text  string
	Known bool
}

// A command is one simple command as the shell would run it, after unwrapping wrappers such as
// env, sudo, xargs and bash -c.
type Command struct {
	Args    []Word
	Assigns []Word // NAME=value prefixes, as "NAME=value"
	Redirs  []Redirect
	Dir     Dir // the directory it runs in
	// Nested is true for a command inside a substitution, so a rule can say so.
	Nested bool
}

type Redirect struct {
	Op     string // >, >>, <, <>, >|, &>, …
	Target Word
}

// dir is the working directory at a point in the command: Logical follows cd as the shell does
// (through links), Physical is what the kernel resolves relative paths against. Known is false
// after cd to a value that cannot be known.
type Dir struct {
	Logical, Physical string
	Known             bool
}

func (c Command) Name() string {
	if len(c.Args) == 0 || !c.Args[0].Known {
		return ""
	}
	return filepath.Base(c.Args[0].Text)
}

// parsed is a whole shell input, reduced to its simple commands.
type Parsed struct {
	Commands []Command
}

// maxDepth bounds bash -c and eval nesting.
const maxDepth = 4

// parse reads a shell command into its simple commands, in order, tracking cd. Anything it cannot
// parse is an error, which the caller turns into a deny.
func Parse(src string, start Dir, env *Env) (*Parsed, error) {
	p := &Parsed{}
	if err := p.parseInto(src, start, env, 0, false); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Parsed) parseInto(src string, start Dir, env *Env, depth int, nested bool) error {
	if depth > maxDepth {
		return fmt.Errorf("the command nests shells more than %d deep", maxDepth)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return fmt.Errorf("the command cannot be parsed (%v)", err)
	}
	w := &walker{p: p, env: env, depth: depth}
	d := start
	for _, st := range f.Stmts {
		if err := w.stmt(st, &d, nested); err != nil {
			return err
		}
	}
	return nil
}

// env is what word evaluation knows: the home directory.
type Env struct {
	Home string
}

type walker struct {
	p     *Parsed
	env   *Env
	depth int
}

// stmt walks one statement. d is the working directory, updated by a top-level cd; constructs that
// run in a subshell get a copy.
func (w *walker) stmt(st *syntax.Stmt, d *Dir, nested bool) error {
	if st == nil {
		return nil
	}
	var redirs []Redirect
	for _, r := range st.Redirs {
		if err := w.substitutions(r.Word, *d); err != nil {
			return err
		}
		redirs = append(redirs, Redirect{Op: r.Op.String(), Target: w.word(r.Word, *d)})
		if r.Hdoc != nil {
			if err := w.substitutions(r.Hdoc, *d); err != nil {
				return err
			}
		}
	}
	if st.Background || st.Coprocess {
		sub := *d
		return w.cmd(st.Cmd, &sub, redirs, nested)
	}
	return w.cmd(st.Cmd, d, redirs, nested)
}

func (w *walker) cmd(c syntax.Command, d *Dir, redirs []Redirect, nested bool) error {
	switch c := c.(type) {
	case nil:
		if len(redirs) > 0 {
			w.p.Commands = append(w.p.Commands, Command{Redirs: redirs, Dir: *d, Nested: nested})
		}
		return nil
	case *syntax.CallExpr:
		return w.call(c, d, redirs, nested)
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			// Each side of a pipe runs in a subshell: a cd there does not persist.
			l, r := *d, *d
			if err := w.stmt(c.X, &l, nested); err != nil {
				return err
			}
			if err := w.stmt(c.Y, &r, nested); err != nil {
				return err
			}
			return w.redirsOnly(redirs, *d, nested)
		}
		// && and ||: the right side may run after the left, in its directory. If the left side's cd
		// may not have run (||), the directory is no longer certain.
		if err := w.stmt(c.X, d, nested); err != nil {
			return err
		}
		if c.Op == syntax.OrStmt {
			d.Known = false
		}
		if err := w.stmt(c.Y, d, nested); err != nil {
			return err
		}
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.Subshell:
		sub := *d
		for _, st := range c.Stmts {
			if err := w.stmt(st, &sub, nested); err != nil {
				return err
			}
		}
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.Block:
		for _, st := range c.Stmts {
			if err := w.stmt(st, d, nested); err != nil {
				return err
			}
		}
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.IfClause:
		// Branches may or may not run: walk them all, and the directory after is uncertain if any
		// of them changes it.
		before := *d
		for cl := c; cl != nil; cl = cl.Else {
			for _, st := range cl.Cond {
				if err := w.stmt(st, d, nested); err != nil {
					return err
				}
			}
			branch := *d
			for _, st := range cl.Then {
				if err := w.stmt(st, &branch, nested); err != nil {
					return err
				}
			}
			if branch != *d {
				d.Known = false
			}
		}
		if *d != before {
			d.Known = false
		}
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.WhileClause:
		return w.loop(append(append([]*syntax.Stmt{}, c.Cond...), c.Do...), d, redirs, nested)
	case *syntax.ForClause:
		switch l := c.Loop.(type) {
		case *syntax.WordIter:
			for _, wd := range l.Items {
				if err := w.substitutions(wd, *d); err != nil {
					return err
				}
			}
		case *syntax.CStyleLoop:
			// Arithmetic only.
		}
		return w.loop(c.Do, d, redirs, nested)
	case *syntax.CaseClause:
		if err := w.substitutions(c.Word, *d); err != nil {
			return err
		}
		var body []*syntax.Stmt
		for _, it := range c.Items {
			body = append(body, it.Stmts...)
		}
		return w.loop(body, d, redirs, nested)
	case *syntax.FuncDecl:
		// The body may run later, from anywhere: walk it as if it ran here, in its own scope.
		sub := *d
		sub.Known = false
		return w.stmt(c.Body, &sub, nested)
	case *syntax.DeclClause:
		// export, declare, local, readonly, typeset: assignments, which may hold paths.
		cmd := Command{Args: []Word{{Text: c.Variant.Value, Known: true}}, Redirs: redirs, Dir: *d, Nested: nested}
		for _, a := range c.Args {
			if a.Value != nil {
				if err := w.substitutions(a.Value, *d); err != nil {
					return err
				}
			}
			if a.Name != nil {
				v := Word{Text: a.Name.Value, Known: true}
				if a.Value != nil {
					val := w.word(a.Value, *d)
					v = Word{Text: a.Name.Value + "=" + val.Text, Known: val.Known}
				} else if a.Naked {
					v = Word{Text: a.Name.Value, Known: true}
				}
				cmd.Args = append(cmd.Args, v)
			} else if a.Value != nil {
				cmd.Args = append(cmd.Args, w.word(a.Value, *d))
			}
		}
		w.p.Commands = append(w.p.Commands, cmd)
		return nil
	case *syntax.TimeClause:
		return w.stmt(c.Stmt, d, nested)
	case *syntax.CoprocClause:
		sub := *d
		return w.stmt(c.Stmt, &sub, nested)
	case *syntax.LetClause, *syntax.ArithmCmd:
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.TestClause:
		var err error
		syntax.Walk(c, func(n syntax.Node) bool {
			if wd, ok := n.(*syntax.Word); ok && err == nil {
				err = w.substitutions(wd, *d)
				return false
			}
			return true
		})
		if err != nil {
			return err
		}
		return w.redirsOnly(redirs, *d, nested)
	}
	return fmt.Errorf("the command uses shell syntax aw does not check (%T)", c)
}

func (w *walker) loop(body []*syntax.Stmt, d *Dir, redirs []Redirect, nested bool) error {
	before := *d
	for _, st := range body {
		if err := w.stmt(st, d, nested); err != nil {
			return err
		}
	}
	if *d != before {
		d.Known = false // a cd in a loop body runs zero or more times
	}
	return w.redirsOnly(redirs, *d, nested)
}

func (w *walker) redirsOnly(redirs []Redirect, d Dir, nested bool) error {
	if len(redirs) > 0 {
		w.p.Commands = append(w.p.Commands, Command{Redirs: redirs, Dir: d, Nested: nested})
	}
	return nil
}

// call records a simple command, unwrapping wrappers, and follows cd.
func (w *walker) call(c *syntax.CallExpr, d *Dir, redirs []Redirect, nested bool) error {
	cmd := Command{Redirs: redirs, Dir: *d, Nested: nested}
	for _, a := range c.Assigns {
		if a.Value != nil {
			if err := w.substitutions(a.Value, *d); err != nil {
				return err
			}
		}
		v := Word{Text: a.Name.Value + "=", Known: true}
		if a.Value != nil {
			val := w.word(a.Value, *d)
			v = Word{Text: a.Name.Value + "=" + val.Text, Known: val.Known}
		}
		if a.Array != nil || a.Index != nil {
			v.Known = false
		}
		cmd.Assigns = append(cmd.Assigns, v)
	}
	for _, a := range c.Args {
		if err := w.substitutions(a, *d); err != nil {
			return err
		}
		cmd.Args = append(cmd.Args, w.word(a, *d))
	}
	return w.record(cmd, d)
}

// record appends a command, or its unwrapped form, and applies a cd to d.
func (w *walker) record(cmd Command, d *Dir) error {
	w.p.Commands = append(w.p.Commands, cmd)
	if len(cmd.Args) == 0 {
		return nil
	}
	name := cmd.Name()
	args := cmd.Args[1:]
	switch name {
	case "cd", "pushd":
		target := ""
		for _, a := range args {
			if a.Known && (a.Text == "-L" || a.Text == "-P" || a.Text == "-e" || a.Text == "-@" || a.Text == "--") {
				continue
			}
			if !a.Known {
				d.Known = false
				return nil
			}
			target = a.Text
			break
		}
		switch target {
		case "":
			target = w.env.Home
		case "-":
			d.Known = false
			return nil
		}
		*d = d.Cd(target)
		return nil
	case "popd":
		d.Known = false
		return nil
	case "bash", "sh", "zsh", "dash", "ksh":
		// sh -c 'script' runs the script; any other form runs a file or reads stdin.
		for i, a := range args {
			if a.Known && strings.HasPrefix(a.Text, "-") && !strings.HasPrefix(a.Text, "--") && strings.Contains(a.Text, "c") {
				if i+1 >= len(args) || !args[i+1].Known {
					return fmt.Errorf("%s -c runs a script aw cannot read", name)
				}
				sub := *d
				return w.p.parseInto(args[i+1].Text, sub, w.env, w.depth+1, cmd.Nested)
			}
		}
	case "eval":
		var parts []string
		for _, a := range args {
			if !a.Known {
				return fmt.Errorf("eval runs a command aw cannot read")
			}
			parts = append(parts, a.Text)
		}
		return w.p.parseInto(strings.Join(parts, " "), *d, w.env, w.depth+1, cmd.Nested)
	}
	if inner, ok := unwrap(name, args); ok {
		next := Command{Args: inner, Dir: cmd.Dir, Nested: cmd.Nested}
		if name == "env" {
			for len(next.Args) > 0 && next.Args[0].Known && strings.Contains(next.Args[0].Text, "=") && !strings.HasPrefix(next.Args[0].Text, "-") {
				next.Assigns = append(next.Assigns, next.Args[0])
				next.Args = next.Args[1:]
			}
		}
		if len(next.Args) > 0 {
			sub := *d
			return w.record(next, &sub)
		}
	}
	return nil
}

// wrappers run their arguments as a command. The value is how many leading arguments, after any
// options, belong to the wrapper itself (timeout 5 cmd: 1).
var wrappers = map[string]int{
	"env": 0, "sudo": 0, "doas": 0, "nohup": 0, "time": 0, "nice": 0, "ionice": 0, "command": 0,
	"exec": 0, "builtin": 0, "xargs": 0, "timeout": 1, "watch": 0, "stdbuf": 0, "setsid": 0,
	"chronic": 0, "unbuffer": 0, "caffeinate": 0, "strace": 0, "ltrace": 0, "flock": 1, "taskset": 1,
	"chrt": 1, "script": 0, "entr": 0, "parallel": 0,
}

// optionsWithValue are wrapper options that take the next argument.
var optionsWithValue = map[string]bool{
	"-u": true, "-g": true, "-n": true, "-s": true, "-k": true, "-I": true, "-L": true, "-P": true,
	"-C": true, "-c": true, "-d": true, "-E": true, "--signal": true, "--kill-after": true, "-i": true,
	"-o": true, "-e": true,
}

// unwrap returns the command a wrapper runs.
func unwrap(name string, args []Word) ([]Word, bool) {
	skip, ok := wrappers[name]
	if !ok {
		return nil, false
	}
	i := 0
	for i < len(args) && args[i].Known && strings.HasPrefix(args[i].Text, "-") {
		if args[i].Text == "--" {
			i++
			break
		}
		if optionsWithValue[args[i].Text] && name != "env" && name != "command" {
			i++
		}
		i++
	}
	if name == "command" && i > 0 && i <= len(args) {
		for _, a := range args[:i] {
			if a.Text == "-v" || a.Text == "-V" {
				return nil, false // command -v: a lookup, not a run
			}
		}
	}
	i += skip
	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

// unescape removes the shell's backslash escapes from a literal: outside quotes every \x is x and
// a backslash-newline is removed; inside double quotes only \$, \`, \", \\ and backslash-newline
// are escapes.
func unescape(s string, dquoted bool) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		next := s[i+1]
		switch {
		case next == '\n':
			i++
		case !dquoted || strings.IndexByte("$`\"\\", next) >= 0:
			b.WriteByte(next)
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Cd moves a directory: logically as the shell's cd does, and physically by resolving links.
func (d Dir) Cd(target string) Dir {
	if !d.Known {
		return d
	}
	next := target
	if !filepath.IsAbs(next) {
		next = filepath.Join(d.Logical, next)
	}
	next = filepath.Clean(next)
	return Dir{Logical: next, Physical: Resolve(next), Known: true}
}

// substitutions walks the commands inside a word's command and process substitutions, so that
// rules see them too.
func (w *walker) substitutions(wd *syntax.Word, d Dir) error {
	if wd == nil {
		return nil
	}
	var err error
	syntax.Walk(wd, func(n syntax.Node) bool {
		if err != nil {
			return false
		}
		switch n := n.(type) {
		case *syntax.CmdSubst:
			sub := d
			for _, st := range n.Stmts {
				if err = w.stmt(st, &sub, true); err != nil {
					return false
				}
			}
			return false
		case *syntax.ProcSubst:
			sub := d
			for _, st := range n.Stmts {
				if err = w.stmt(st, &sub, true); err != nil {
					return false
				}
			}
			return false
		}
		return true
	})
	return err
}

// word evaluates a word as far as it can be known.
func (w *walker) word(wd *syntax.Word, d Dir) Word {
	if wd == nil {
		return Word{Known: true}
	}
	var b strings.Builder
	known := true
	for i, part := range wd.Parts {
		text, ok := w.part(part, d, i == 0)
		b.WriteString(text)
		known = known && ok
	}
	return Word{Text: b.String(), Known: known}
}

func (w *walker) part(p syntax.WordPart, d Dir, first bool) (string, bool) {
	switch p := p.(type) {
	case *syntax.Lit:
		v := unescape(p.Value, false)
		if first && (v == "~" || strings.HasPrefix(v, "~/")) {
			return w.env.Home + v[1:], w.env.Home != ""
		}
		if first && strings.HasPrefix(v, "~") {
			return v, false // ~user
		}
		return v, true
	case *syntax.SglQuoted:
		return p.Value, !p.Dollar || !strings.Contains(p.Value, `\`)
	case *syntax.DblQuoted:
		var b strings.Builder
		known := true
		for _, q := range p.Parts {
			if lit, isLit := q.(*syntax.Lit); isLit {
				b.WriteString(unescape(lit.Value, true))
				continue
			}
			t, ok := w.part(q, d, false)
			b.WriteString(t)
			known = known && ok
		}
		return b.String(), known
	case *syntax.ParamExp:
		if p.Param == nil || p.Excl || p.Length || p.Index != nil || p.Slice != nil || p.Repl != nil || p.Exp != nil || p.Names != 0 || p.Width {
			return "", false
		}
		switch p.Param.Value {
		case "HOME":
			return w.env.Home, w.env.Home != ""
		case "PWD":
			return d.Logical, d.Known
		}
		return "", false
	case *syntax.CmdSubst:
		// $(pwd) is the one substitution whose value is known.
		if len(p.Stmts) == 1 && len(p.Stmts[0].Redirs) == 0 {
			if call, ok := p.Stmts[0].Cmd.(*syntax.CallExpr); ok && len(call.Assigns) == 0 && len(call.Args) == 1 {
				if lit := call.Args[0].Lit(); lit == "pwd" {
					return d.Logical, d.Known
				}
			}
		}
		return "", false
	}
	return "", false
}

// Resolve makes an absolute, clean path physical: links are resolved in the longest prefix that
// exists, and the rest is appended, so a path to a file not yet created still resolves through a
// linked directory.
func Resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
