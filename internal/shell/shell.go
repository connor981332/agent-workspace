// Package shell parses a shell command into the simple commands it would run, for guard decisions
// (architecture.md, "Policy engine", step 3). It never runs anything: words whose value depends on
// an expansion are marked unknown, and callers fail closed on them.
//
// This package is on the hot path: it may not import os/exec or net.
package shell

import (
	"fmt"
	"path/filepath"
	"strconv"
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
	if err := p.parseInto(src, start, env, 0, false, nil); err != nil {
		return nil, err
	}
	return p, nil
}

// parseInto walks src. sc is the shell it runs in: eval's, or nil for a new shell (the whole input,
// or sh -c), which then also walks the functions it defines but never calls.
func (p *Parsed) parseInto(src string, start Dir, env *Env, depth int, nested bool, sc *scope) error {
	if depth > maxDepth {
		return fmt.Errorf("the command nests shells more than %d deep", maxDepth)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return fmt.Errorf("the command cannot be parsed (%v)", err)
	}
	own := sc == nil
	if own {
		sc = newScope()
	}
	w := &walker{p: p, env: env, depth: depth, sc: sc}
	d := start
	for _, st := range f.Stmts {
		if err := w.stmt(st, &d, nested); err != nil {
			return err
		}
	}
	if own {
		return w.uncalled(nested)
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
	sc    *scope
}

// scope is what the shell knows at a point of the command: the variables assigned earlier in it,
// with their values as far as they can be known, and the functions it defined.
//
// A variable set in a part that may not run (a branch, a loop body, the right of && or ||) is
// unknown after it, and in a loop it is unknown from the start of every pass, since an earlier pass
// may have changed it. A variable never assigned here comes from the environment, and is unknown.
type scope struct {
	vars  map[string]Word
	funcs map[string]*function
	defs  *[]*function // every function defined, in any subshell, for uncalled
}

type function struct {
	body   *syntax.Stmt
	called bool
	active bool // being walked, to stop recursion
}

func newScope() *scope {
	return &scope{vars: map[string]Word{}, funcs: map[string]*function{}, defs: &[]*function{}}
}

// clone is the scope of a subshell: what it changes does not come back.
func (sc *scope) clone() *scope {
	c := &scope{vars: make(map[string]Word, len(sc.vars)), funcs: make(map[string]*function, len(sc.funcs)), defs: sc.defs}
	for k, v := range sc.vars {
		c.vars[k] = v
	}
	for k, v := range sc.funcs {
		c.funcs[k] = v // shared, so a call in a subshell counts as a call
	}
	return c
}

// subshell runs f in a copy of the scope.
func (w *walker) subshell(f func() error) error {
	saved := w.sc
	w.sc = saved.clone()
	defer func() { w.sc = saved }()
	return f()
}

// forget makes unknown every variable n assigns, anywhere inside it.
func (w *walker) forget(n syntax.Node) {
	if n == nil {
		return
	}
	syntax.Walk(n, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Assign:
			if n.Name != nil {
				w.sc.vars[n.Name.Value] = Word{}
			}
		case *syntax.WordIter:
			if n.Name != nil {
				w.sc.vars[n.Name.Value] = Word{}
			}
		case *syntax.CallExpr:
			w.forgetSetBy(n)
		}
		return true
	})
}

// forgetSetBy makes unknown what a builtin sets by name (read A, printf -v A, unset A), and, for a
// command that can set anything (source, a function), everything.
func (w *walker) forgetSetBy(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	switch name := c.Args[0].Lit(); name {
	case "read", "mapfile", "readarray", "getopts", "printf", "unset", "let", "export", "declare", "typeset", "local", "readonly":
		for _, a := range c.Args[1:] {
			if lit := a.Lit(); lit != "" {
				n, _, _ := strings.Cut(lit, "=")
				w.sc.vars[n] = Word{}
			}
		}
	case "source", ".":
		w.forgetAll()
	default:
		if _, ok := w.sc.funcs[name]; ok {
			w.forgetAll()
		}
	}
}

func (w *walker) forgetAll() {
	for k := range w.sc.vars {
		w.sc.vars[k] = Word{}
	}
}

// uncalled walks the bodies of functions defined but never called here. A shell that persists
// between tool calls could call one later, from anywhere, with any arguments.
func (w *walker) uncalled(nested bool) error {
	for _, f := range *w.sc.defs {
		if f.called {
			continue
		}
		f.called = true
		sub := Dir{}
		if err := w.subshell(func() error {
			w.forgetAll()
			for i := range 10 {
				w.sc.vars[strconv.Itoa(i)] = Word{}
			}
			return w.stmt(f.body, &sub, nested)
		}); err != nil {
			return err
		}
	}
	return nil
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
		return w.subshell(func() error { return w.cmd(st.Cmd, &sub, redirs, nested) })
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
			if err := w.subshell(func() error { return w.stmt(c.X, &l, nested) }); err != nil {
				return err
			}
			if err := w.subshell(func() error { return w.stmt(c.Y, &r, nested) }); err != nil {
				return err
			}
			return w.redirsOnly(redirs, *d, nested)
		}
		if c.Op == syntax.AndStmt {
			// a && b && c: each part runs after the ones before it succeeded, in their directory and
			// with their variables. After the chain, what a later part set may not have happened.
			parts := andChain(c)
			for _, st := range parts {
				if err := w.stmt(st, d, nested); err != nil {
					return err
				}
			}
			for _, st := range parts[1:] {
				w.forget(st)
			}
			return w.redirsOnly(redirs, *d, nested)
		}
		// a || b: b runs when a failed, perhaps after a's cd. After it, the directory is a's or b's.
		before := *d
		if err := w.stmt(c.X, d, nested); err != nil {
			return err
		}
		if *d != before {
			d.Known = false
		}
		right := *d
		if err := w.stmt(c.Y, &right, nested); err != nil {
			return err
		}
		if right != *d {
			d.Known = false
		}
		w.forget(c.Y)
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.Subshell:
		sub := *d
		if err := w.subshell(func() error {
			for _, st := range c.Stmts {
				if err := w.stmt(st, &sub, nested); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
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
			if err := w.subshell(func() error {
				for _, st := range cl.Then {
					if err := w.stmt(st, &branch, nested); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if branch != *d {
				d.Known = false
			}
		}
		if *d != before {
			d.Known = false
		}
		w.forget(c)
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.WhileClause:
		w.forget(c)
		err := w.loop(append(append([]*syntax.Stmt{}, c.Cond...), c.Do...), d, redirs, nested)
		w.forget(c)
		return err
	case *syntax.ForClause:
		l, ok := c.Loop.(*syntax.WordIter)
		if !ok { // a C-style loop: arithmetic only
			w.forget(c)
			err := w.loop(c.Do, d, redirs, nested)
			w.forget(c)
			return err
		}
		for _, wd := range l.Items {
			if err := w.substitutions(wd, *d); err != nil {
				return err
			}
		}
		// for x in a b c: walk the body once for each value, when the list can be known and is short.
		values, known := w.items(l.Items, *d)
		if !known || len(values) == 0 || len(values) > maxItems {
			values = []Word{{}}
		}
		for _, v := range values {
			w.forget(c)
			w.sc.vars[l.Name.Value] = v
			if err := w.loop(c.Do, d, nil, nested); err != nil {
				return err
			}
		}
		w.forget(c)
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.CaseClause:
		if err := w.substitutions(c.Word, *d); err != nil {
			return err
		}
		before := *d
		for _, it := range c.Items {
			branch := *d
			if err := w.subshell(func() error {
				for _, st := range it.Stmts {
					if err := w.stmt(st, &branch, nested); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if branch != before {
				d.Known = false
			}
		}
		w.forget(c)
		return w.redirsOnly(redirs, *d, nested)
	case *syntax.FuncDecl:
		// The body is walked where the function is called, with that call's directory and arguments,
		// or at the end if it is never called (uncalled).
		f := &function{body: c.Body}
		w.sc.funcs[c.Name.Value] = f
		*w.sc.defs = append(*w.sc.defs, f)
		return w.redirsOnly(redirs, *d, nested)
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
					if a.Array != nil || a.Index != nil || a.Append {
						val = Word{}
					}
					w.sc.vars[a.Name.Value] = val
				} else if a.Naked {
					v = Word{Text: a.Name.Value, Known: true}
				} else {
					w.sc.vars[a.Name.Value] = Word{}
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
		return w.subshell(func() error { return w.stmt(c.Stmt, &sub, nested) })
	case *syntax.LetClause, *syntax.ArithmCmd:
		w.forget(c) // arithmetic can assign: ((i = 2))
		forgetArithm(w, c)
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
	if len(c.Args) == 0 {
		// A=1 B=$A: shell variables, set in order. As a prefix of a command they reach only its
		// environment, and the shell's own variables are unchanged.
		for i, a := range c.Assigns {
			_, val, _ := strings.Cut(cmd.Assigns[i].Text, "=")
			w.sc.vars[a.Name.Value] = Word{Text: val, Known: cmd.Assigns[i].Known && !a.Append}
		}
	}
	if f, ok := w.sc.funcs[cmd.Name()]; ok && len(cmd.Args) > 0 && cmd.Args[0].Known {
		// The body is what runs; the call's own redirects apply to all of it.
		if err := w.redirsOnly(redirs, *d, nested); err != nil {
			return err
		}
		return w.callFunction(f, cmd, d, nested)
	}
	w.forgetSetBy(c)
	return w.record(cmd, d)
}

// callFunction walks a function's body where it is called: in this directory, with the call's
// arguments as $1, $2 and so on. Afterwards what the body set is unknown, since local variables
// end with it and a return may cut it short.
func (w *walker) callFunction(f *function, cmd Command, d *Dir, nested bool) error {
	if f.active {
		return fmt.Errorf("the function %s calls itself, which aw does not follow", cmd.Name())
	}
	f.called, f.active = true, true
	defer func() { f.active = false }()
	saved := map[string]Word{}
	for i := range 10 {
		k := strconv.Itoa(i)
		saved[k] = w.sc.vars[k]
		w.sc.vars[k] = Word{}
		if i > 0 && i < len(cmd.Args) {
			w.sc.vars[k] = cmd.Args[i]
		}
	}
	before := *d
	err := w.stmt(f.body, d, nested)
	w.forget(f.body)
	for k, v := range saved {
		w.sc.vars[k] = v
	}
	if *d != before {
		d.Known = false // a cd in a function persists, unless a return came first
	}
	return err
}

// andChain is the parts of a && b && c, in order.
func andChain(c *syntax.BinaryCmd) []*syntax.Stmt {
	var parts []*syntax.Stmt
	if x, ok := c.X.Cmd.(*syntax.BinaryCmd); ok && x.Op == syntax.AndStmt && len(c.X.Redirs) == 0 && !c.X.Negated && !c.X.Background {
		parts = andChain(x)
	} else {
		parts = []*syntax.Stmt{c.X}
	}
	return append(parts, c.Y)
}

// maxItems bounds how many values of a for loop are walked one by one.
const maxItems = 32

// items are the values of a for loop's word list: each word as far as it can be known, with a glob
// over a known directory expanded as the shell would.
func (w *walker) items(words []*syntax.Word, d Dir) ([]Word, bool) {
	var out []Word
	for _, wd := range words {
		v := w.word(wd, d)
		if !v.Known {
			return nil, false
		}
		if !unquotedGlob(wd) {
			out = append(out, v)
			continue
		}
		pattern := v.Text
		if !filepath.IsAbs(pattern) {
			if !d.Known {
				return nil, false
			}
			pattern = filepath.Join(d.Logical, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, false
		}
		if len(matches) == 0 {
			out = append(out, v) // no match: the shell keeps the word as written
			continue
		}
		for _, m := range matches {
			if !filepath.IsAbs(v.Text) {
				m, _ = filepath.Rel(d.Logical, m)
			}
			out = append(out, Word{Text: m, Known: true})
		}
	}
	return out, true
}

// unquotedGlob reports whether a word has a glob character outside quotes.
func unquotedGlob(wd *syntax.Word) bool {
	for _, p := range wd.Parts {
		if lit, ok := p.(*syntax.Lit); ok && strings.ContainsAny(lit.Value, "*?[") {
			return true
		}
	}
	return false
}

// forgetArithm makes unknown the variables an arithmetic command can assign.
func forgetArithm(w *walker, n syntax.Node) {
	syntax.Walk(n, func(n syntax.Node) bool {
		if v, ok := n.(*syntax.ParamExp); ok && v.Param != nil {
			w.sc.vars[v.Param.Value] = Word{}
		}
		if v, ok := n.(*syntax.Lit); ok {
			w.sc.vars[v.Value] = Word{}
		}
		return true
	})
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
				return w.p.parseInto(args[i+1].Text, sub, w.env, w.depth+1, cmd.Nested, nil)
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
		return w.p.parseInto(strings.Join(parts, " "), *d, w.env, w.depth+1, cmd.Nested, w.sc)
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
		case *syntax.ArithmExp:
			forgetArithm(w, n) // $((i = 2)) assigns
			return false
		case *syntax.ParamExp:
			if n.Exp != nil && n.Param != nil {
				w.sc.vars[n.Param.Value] = Word{} // ${A:=x} assigns
			}
			return true
		case *syntax.CmdSubst:
			sub := d
			err = w.subshell(func() error {
				for _, st := range n.Stmts {
					if err := w.stmt(st, &sub, true); err != nil {
						return err
					}
				}
				return nil
			})
			return false
		case *syntax.ProcSubst:
			sub := d
			err = w.subshell(func() error {
				for _, st := range n.Stmts {
					if err := w.stmt(st, &sub, true); err != nil {
						return err
					}
				}
				return nil
			})
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
		text, ok := w.part(part, d, i == 0, false)
		b.WriteString(text)
		known = known && ok
	}
	return Word{Text: b.String(), Known: known}
}

func (w *walker) part(p syntax.WordPart, d Dir, first, quoted bool) (string, bool) {
	switch p := p.(type) {
	case *syntax.Lit:
		v := unescape(p.Value, false)
		if braceExpansion(p.Value) {
			return v, false // a{b,c} is two words, a{1..3} three: what each is, is not this text
		}
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
			t, ok := w.part(q, d, false, true)
			b.WriteString(t)
			known = known && ok
		}
		return b.String(), known
	case *syntax.ParamExp:
		if p.Param == nil || p.Excl || p.Length || p.Index != nil || p.Slice != nil || p.Repl != nil || p.Exp != nil || p.Names != 0 || p.Width {
			return "", false
		}
		if v, ok := w.sc.vars[p.Param.Value]; ok {
			// Unquoted, the shell splits a value on spaces and expands globs in it, and an empty one
			// vanishes: then it is not this one word.
			if !quoted && (v.Text == "" || strings.ContainsAny(v.Text, " \t\n*?[")) {
				return v.Text, false
			}
			return v.Text, v.Known
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

// braceExpansion reports whether an unquoted literal has a brace expansion: {a,b} or {1..3}.
func braceExpansion(lit string) bool {
	for i := strings.IndexByte(lit, '{'); i >= 0; {
		rest := lit[i+1:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return false
		}
		if inner := rest[:end]; strings.Contains(inner, ",") || strings.Contains(inner, "..") {
			return true
		}
		next := strings.IndexByte(rest, '{')
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
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
