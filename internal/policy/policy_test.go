package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture lays out a workspace as aw does: the owner checkout with two projects, lane 01 whose root
// links to its project worktrees, lane 02, and lane 01's private state directory.
type fixture struct {
	owner, lane, state, home string
}

func newFixture(t testing.TB) fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		owner: filepath.Join(base, "it's a workspace"),
		home:  filepath.Join(base, "home"),
	}
	f.lane = filepath.Join(f.owner, ".worktrees", "01")
	f.state = filepath.Join(f.owner, ".git", "aw", "lanes", "01")
	for _, d := range []string{
		f.owner, f.lane, f.state, filepath.Join(f.owner, ".worktrees", "02"),
		filepath.Join(f.owner, ".git", "aw", "lanes", "02"),
		filepath.Join(f.owner, "api", ".worktrees", "01", "src"), filepath.Join(f.owner, "web", ".worktrees", "01"),
		filepath.Join(f.home, ".claude"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"api":    "../../api/.worktrees/01",
		"web":    "../../web/.worktrees/01",
		"escape": "../..",
	} {
		if err := os.Symlink(target, filepath.Join(f.lane, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(f.owner, "AGENTS.md"), filepath.Join(f.lane, "AGENTS.md"), filepath.Join(f.owner, "api", ".env")} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(f.owner, "api", ".env"), filepath.Join(f.owner, "api", ".worktrees", "01", ".env")); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) scope(role Role) Scope {
	s := Scope{Role: role, Owner: f.owner, Home: f.home, LaneName: "checkout-tax",
		Secrets: []string{filepath.Join(f.owner, "api", ".env"), filepath.Join(f.owner, "api", ".worktrees", "01", ".env")},
		Servers: []ServerCommand{{Name: "web.dev", Words: []string{"npm", "run", "dev", "--", "--port", "{port}"}}},
	}
	if role == Lane {
		s.LaneRoot = f.lane
		s.LaneDirs = []string{f.lane, filepath.Join(f.owner, "api", ".worktrees", "01"), filepath.Join(f.owner, "web", ".worktrees", "01"), f.state}
	}
	return s
}

type tcase struct {
	cmd  string
	want Verdict
	rule string // when denied or asked, the rule
}

func run(t *testing.T, s Scope, cwd string, cases []tcase) {
	t.Helper()
	for _, tc := range cases {
		cmd := strings.NewReplacer("{owner}", quote(s.Owner), "{lane}", quote(s.LaneRoot)).Replace(tc.cmd)
		d := Decide(s, Call{Tool: Shell, Cwd: cwd, Command: cmd})
		if d.Verdict != tc.want || (tc.rule != "" && d.Rule != tc.rule) {
			t.Errorf("%s\n\tgot %s (%s: %s), want %s %s", cmd, d.Verdict, d.Rule, d.Message, tc.want, tc.rule)
		}
	}
}

// TestBoundary is C1's test_boundary, in this layout: a lane root whose projects are links to their
// worktrees in the owner checkout.
func TestBoundary(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Lane)
	state := quote(f.state)
	other := quote(filepath.Join(f.owner, ".git", "aw", "lanes", "02"))
	run(t, s, f.lane, []tcase{
		{"cat {lane}/file", Allow, ""},
		{"cat {lane}/file {owner}/file", Deny, "boundary"},
		{"cat {owner}/.worktrees/02/file", Deny, "boundary"},
		{"cat ../../AGENTS.md", Deny, "boundary"},
		{"cat escape/AGENTS.md", Deny, "boundary"},
		// cd follows the link logically, as the shell does, so .. returns to the lane root.
		{"cd api && cd .. && cat AGENTS.md", Allow, ""},
		// Changed from C1: a file argument is resolved physically, as the kernel does: from the api
		// worktree, ../AGENTS.md is in the owner's api directory, not the lane root.
		{"cd api && cat ../AGENTS.md", Deny, "boundary"},
		{"cd api && cat src/x.go", Allow, ""},
		{"cd api && cat ../../../AGENTS.md", Deny, "boundary"},
		{"cat " + state + "/api.log", Allow, ""},
		{"cat " + other + "/api.env", Deny, "boundary"},
		{"bin/aw run api npm test -- test/unit/example.ts", Allow, ""},
		{"cat /tmp/claude-scratch/out.txt ~/.claude/notes.md", Allow, ""},
		{"echo $(pwd)/x 2>/dev/null", Allow, ""},
		{"echo \"$PWD/x\" > out.txt", Allow, ""},
		{"echo hi > {owner}/x", Deny, "boundary"},
		{"cp a.txt --target-directory={owner}", Deny, "boundary"}, // an option's value is a path too
		{"cp a.txt --target-directory=api", Allow, ""},
		{"cat it\\'s/../../../AGENTS.md", Deny, "boundary"}, // backslash escapes are resolved first
		{"cat \"../..\"/AGENTS.md", Deny, "boundary"},
		{"git -C {owner} status", Deny, "boundary"},
		{"git -C api status", Allow, ""},
		{"make -C ../.. build", Deny, "boundary"},
		{"cd {owner} && ls", Deny, "boundary"},
		{"cd /tmp && rm -rf x", Allow, ""},
		{"cd \"$SOMEWHERE\" && rm -rf x", Deny, "boundary"},
		{"cat $DIR/file", Deny, "boundary"},
		{"cat \"$(git rev-parse --show-toplevel)/AGENTS.md\"", Deny, "boundary"},
		{"(cd {owner} && ls)", Deny, "boundary"},
		{"ls ../*", Deny, "boundary"},
		{"OUT=../../x make", Deny, "boundary"},
		{"bash -c 'cat ../../AGENTS.md'", Deny, "boundary"},
		{"bash -c \"$CMD\"", Deny, "parse"},
		{"echo $(cat ../../AGENTS.md)", Deny, "boundary"},
		{"diff <(cat ../../AGENTS.md) AGENTS.md", Deny, "boundary"},
		{"curl https://example.com/a/b > /tmp/page", Allow, ""},
		{"for f in a b; do cd $f; done; ls", Deny, "boundary"},
		{"if true; then cd /tmp; fi; rm x", Deny, "boundary"},
		{"echo 'unterminated", Deny, "parse"},
	})
	run(t, s, f.lane, []tcase{{"{owner}/bin/aw whoami", Allow, ""}, {"{owner}/bin/aw discard x", Deny, "owner-only"}})
	// Running from the owner checkout: every command starts with cd into the lane.
	run(t, s, f.owner, []tcase{
		{"bin/aw attach x", Allow, ""},
		{"bin/aw whoami --json", Allow, ""},
		{"bin/aw attach x && rm -rf y", Deny, "boundary"},
		{"bin/aw run web npm test", Deny, "boundary"},
		{"git status", Deny, "boundary"},
		{"cd {lane} && git status", Allow, ""},
		{"cd {lane} && cat ../../AGENTS.md", Deny, "boundary"},
		{"cd {owner} && git status", Deny, "boundary"},
	})
	for _, tc := range []struct {
		paths []string
		want  Verdict
	}{
		{[]string{filepath.Join(f.lane, "x")}, Allow},
		{[]string{"x"}, Deny},
		{nil, Deny},
		{[]string{filepath.Join(f.owner, "x")}, Deny},
	} {
		if d := Decide(s, Call{Tool: Read, Cwd: f.owner, Paths: tc.paths}); d.Verdict != tc.want {
			t.Errorf("from the owner checkout, reading %v: %v", tc.paths, d)
		}
	}
	// The working directory itself.
	if d := Decide(s, Call{Tool: Shell, Cwd: f.owner, Command: "ls"}); d.Verdict != Deny || d.Rule != "boundary" {
		t.Errorf("a lane session in the owner checkout: %v", d)
	}
	// From a project worktree, by its real path.
	if d := Decide(s, Call{Tool: Shell, Cwd: filepath.Join(f.owner, "api", ".worktrees", "01"), Command: "cat src/x.go"}); d.Verdict != Allow {
		t.Errorf("in the lane's api worktree: %v", d)
	}
}

func TestFileTools(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Lane)
	for _, tc := range []struct {
		tool Tool
		path string
		want Verdict
	}{
		{Read, f.owner, Deny},
		{Write, filepath.Join(f.owner, "AGENTS.md"), Deny},
		{Read, filepath.Join(f.lane, "AGENTS.md"), Allow},
		{Write, "api/src/new.go", Allow},
		{Read, "escape/AGENTS.md", Deny},
		{Read, "/tmp/tool-output.txt", Allow},
		{Read, "~/.claude/notes.md", Allow},
		{Read, "api/.env", Deny},
		{Write, "api/.env", Deny},
	} {
		d := Decide(s, Call{Tool: tc.tool, Cwd: f.lane, Paths: []string{tc.path}})
		if d.Verdict != tc.want {
			t.Errorf("%v %s: %v, want %v", tc.tool, tc.path, d, tc.want)
		}
	}
}

func TestUnattached(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Unattached)
	run(t, s, f.owner, []tcase{
		{"ls -la", Allow, ""},
		{"git status && git log --oneline -5 | head", Allow, ""},
		{"rg TODO web | sort | uniq -c > /dev/null", Allow, ""},
		{"cat AGENTS.md; echo done", Allow, ""},
		{"find . -name '*.go'", Allow, ""},
		{"git branch", Allow, ""},
		{"bin/aw attach 'fix the tax'", Allow, ""},
		{"bin/aw whoami --json", Allow, ""},
		{"echo hi > notes.txt", Deny, "attach"},
		{"npm test", Deny, "attach"},
		{"git commit -m x", Deny, "attach"},
		{"sed -i s/a/b/ x", Deny, "attach"},
		{"find . -delete", Deny, "attach"},
		{"ls $(touch x)", Deny, "attach"},
		{"git branch new-thing", Deny, "attach"},
	})
	if d := Decide(s, Call{Tool: Write, Cwd: f.owner, Paths: []string{"x.go"}}); d.Verdict != Deny || !strings.Contains(d.Message, "bin/aw attach") || !strings.Contains(d.Message, "aw pair") {
		t.Errorf("an unattached write: %v", d)
	}
	if d := Decide(s, Call{Tool: Read, Cwd: f.owner, Paths: []string{"AGENTS.md"}}); d.Verdict != Allow {
		t.Errorf("an unattached read: %v", d)
	}
}

func TestCommandRules(t *testing.T) {
	f := newFixture(t)
	for _, role := range []Role{Lane, Pair} {
		s := f.scope(role)
		cwd := f.lane
		if role == Pair {
			cwd = f.owner
		}
		run(t, s, cwd, []tcase{
			// Owner-only aw commands, wherever they appear.
			{"bin/aw discard checkout-tax", Deny, "owner-only"},
			{"echo $(bin/aw pair)", Deny, "owner-only"},
			{"true | aw park x", Deny, "owner-only"},
			{"env FOO=1 bin/aw delegate --for 1h", Deny, "owner-only"},
			{"bin/aw sweep", Allow, ""},
			{"bin/aw sweep --apply", Deny, "owner-only"},
			{"bin/aw finish --summary done", Allow, ""},
			// Destructive Git.
			{"git push --force origin aw/x", Deny, "git"},
			{"git push -f", Deny, "git"},
			{"git push origin +aw/x", Deny, "git"},
			{"git push origin :main", Deny, "git"},
			{"git push --delete origin x", Deny, "git"},
			{"git branch -D main", Deny, "git"},
			{"git reset --hard HEAD~1", Deny, "git"},
			{"git clean -fdx", Deny, "git"},
			{"git stash drop", Deny, "git"},
			{"git tag -d v1", Deny, "git"},
			{"git update-ref -d refs/heads/main", Deny, "git"},
			{"git -c core.hooksPath=/dev/null commit -m x", Deny, "git"},
			{"git config user.name x", Deny, "git"},
			{"git config --global core.editor vim", Deny, "git"},
			{"git config --get user.name", Allow, ""},
			{"git config user.name", Allow, ""},
			{"git worktree add /tmp/x", Deny, "git"},
			{"git worktree list", Allow, ""},
			{"git remote add fork x", Deny, "git"},
			{"git commit -m 'reset --hard'", Allow, ""},
			{"git reset HEAD file", Allow, ""},
			// Containers.
			{"docker run --rm alpine", Deny, "containers"},
			{"docker compose up -d", Deny, "containers"},
			{"docker compose down", Deny, "containers"},
			{"docker rm -f db", Deny, "containers"},
			{"docker stop db", Deny, "containers"},
			{"devcontainer up --workspace-folder .", Deny, "containers"},
			{"docker ps", Allow, ""},
			{"docker compose logs web", Allow, ""},
			{"docker exec db psql -c 'select 1'", Allow, ""},
			// Processes.
			{"pkill -f rails", Deny, "processes"},
			{"kill 1234", Deny, "processes"},
			{"lsof -ti :3000 | xargs kill -9", Deny, "processes"},
			{"killall node", Deny, "processes"},
			{"fuser -k 3000/tcp", Deny, "processes"},
			{"kill -l", Allow, ""},
			// Servers.
			{"npm run dev -- --port 3000", Deny, "servers"},
			{"npm run dev", Deny, "servers"},
			{"nohup npm run dev -- --host 0.0.0.0 &", Deny, "servers"},
			{"npm run build", Allow, ""},
			// Secrets.
			{"env", Deny, "secrets"},
			{"printenv", Deny, "secrets"},
			{"printenv AWS_SECRET", Deny, "secrets"},
			{"cat /proc/self/environ", Deny, "secrets"},
			{"export -p", Deny, "secrets"},
			{"env FOO=1 npm test", Allow, ""},
			{"echo \"$PATH\"", Allow, ""},
			// Publication asks (and is a deny where the harness cannot ask).
			{"git push origin aw/x", Deny, "publication"},
			{"gh pr create --fill", Deny, "publication"},
			{"glab mr merge 3", Deny, "publication"},
			{"npm publish", Deny, "publication"},
			{"gh pr view 3", Allow, ""},
		})
	}
	s := f.scope(Lane)
	run(t, s, f.lane, []tcase{
		{"cat api/.env", Deny, "secrets"},
		{"source api/.env", Deny, "secrets"},
		{"grep KEY < api/.env", Deny, "secrets"},
		{"cp api/.env /tmp/x", Deny, "secrets"},
		{"echo KEY=1 >> api/.env", Deny, "secrets"},
	})
	s.AskSupported = true
	run(t, s, f.lane, []tcase{{"git push origin aw/x", Ask, "publication"}})
}

func TestPairSelfModification(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Pair)
	s.AskSupported = true
	for path, want := range map[string]Verdict{
		"workspace.toml":        Ask,
		"AGENTS.md":             Ask,
		".claude/settings.json": Ask,
		"web/src/app.ts":        Allow,
	} {
		if d := Decide(s, Call{Tool: Write, Cwd: f.owner, Paths: []string{path}}); d.Verdict != want {
			t.Errorf("pair write %s: %v, want %v", path, d, want)
		}
	}
	if d := Decide(s, Call{Tool: Shell, Cwd: f.owner, Command: "echo hi > notes.txt"}); d.Verdict != Allow {
		t.Errorf("a pair session writes in the owner checkout: %v", d)
	}
}

func TestDenyMessagesQuotePaths(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Lane)
	d := Decide(s, Call{Tool: Shell, Cwd: f.lane, Command: "cat " + quote(f.owner+"/AGENTS.md")})
	if d.Verdict != Deny || !strings.Contains(d.Message, quote(f.owner+"/AGENTS.md")) {
		t.Errorf("the message does not quote the path: %s", d.Message)
	}
}

func TestErrorsDeny(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Lane)
	if d := Decide(s, Call{Tool: Shell, Cwd: "relative", Command: "ls"}); d.Verdict != Deny {
		t.Errorf("a relative working directory: %v", d)
	}
	deep := "ls"
	for range 6 {
		deep = "bash -c " + quote(deep)
	}
	if d := Decide(s, Call{Tool: Shell, Cwd: f.lane, Command: deep}); d.Verdict != Deny {
		t.Errorf("nesting beyond the limit: %v", d)
	}
}

func BenchmarkDecideShell(b *testing.B) {
	f := newFixture(b)
	s := f.scope(Lane)
	cmd := `cd web && npm run test -- --grep "checkout tax" 2>&1 | tee /tmp/out.log; git status --porcelain`
	for b.Loop() {
		Decide(s, Call{Tool: Shell, Cwd: f.lane, Command: cmd})
	}
}

func TestPairShellSelfModification(t *testing.T) {
	f := newFixture(t)
	s := Scope{Role: Pair, Owner: f.owner, Home: f.home, AskSupported: true}
	for cmd, want := range map[string]Verdict{
		"echo hi > notes.txt":             Allow,
		"cp a.txt b.txt":                  Allow,
		"cp AGENTS.md /tmp/x":             Allow,
		"rm -rf build":                    Allow,
		"cat workspace.toml":              Allow,
		"sed s/a/b/ workspace.toml":       Allow,
		"echo x 2>&1":                     Allow,
		"echo x > /dev/null":              Allow,
		"echo x > workspace.toml":         Ask,
		"echo x >> CLAUDE.md":             Ask,
		"cp a.txt AGENTS.md":              Ask,
		"cp -t .aw a.txt":                 Ask,
		"mv AGENTS.md old":                Ask,
		"rm bin/aw":                       Ask,
		"sed -i s/a/b/ workspace.toml":    Ask,
		"dd if=x of=.claude/settings":     Ask,
		"ln -s x .githooks/pre-push":      Ask,
		"touch \"$X\"":                    Ask,
		"cd api && echo x > ../AGENTS.md": Ask,
		"echo x > .git/config":            Deny,
		"rm -rf .git/aw":                  Deny,
		"echo x > api/.git/config":        Deny,
		"sqlite3 .git/aw/state.db x":      Deny,
	} {
		if d := Decide(s, Call{Tool: Shell, Cwd: f.owner, Command: cmd}); d.Verdict != want {
			t.Errorf("%s: got %v (%s), want %v", cmd, d.Verdict, d.Message, want)
		}
	}
}

func TestTeamGuards(t *testing.T) {
	f := newFixture(t)
	guards := []TeamGuard{
		{Name: "apply", Commands: [][]string{{"terraform", "apply"}, {"git", "push", "--tags"}}, Message: "no"},
		{Name: "schema", Paths: []string{"web/db/schema.rb", "**/*.lock"}, Message: "generated"},
		{Name: "gone", Commands: [][]string{{"deploy"}}, Ask: true, Message: "ask", Requires: []string{"missing/file"}},
	}
	pair := Scope{Role: Pair, Owner: f.owner, Home: f.home, AskSupported: true, Guards: guards}
	for cmd, want := range map[string]Verdict{
		"terraform plan":                 Allow,
		"terraform apply":                Deny,
		"/usr/bin/terraform apply -lock": Deny,
		"terraform -chdir=x apply":       Deny,
		"git push origin":                Ask, // the publication policy, not the guard
		"git push --tags":                Deny,
		"deploy now":                     Deny, // requirement unmet: an ask guard refuses
		"echo x > web/db/schema.rb":      Deny,
		"echo x > web/db/other.rb":       Allow,
		"echo x > a/b/pnpm.lock":         Deny,
		"rm web/db/schema.rb":            Deny,
		"cat web/db/schema.rb":           Allow,
		"echo x > \"$OUT\"":              Deny, // it could be the guarded file
	} {
		if d := Decide(pair, Call{Tool: Shell, Cwd: f.owner, Command: cmd}); d.Verdict != want {
			t.Errorf("%s: got %v (%s), want %v", cmd, d.Verdict, d.Message, want)
		}
	}
	lane := Scope{Role: Lane, Owner: f.owner, Home: f.home, LaneRoot: f.lane, LaneName: "x", Guards: guards,
		LaneDirs: []string{f.lane, f.state, filepath.Join(f.owner, "web", ".worktrees", "01"), filepath.Join(f.owner, "api", ".worktrees", "01")}}
	if d := Decide(lane, Call{Tool: Write, Cwd: f.lane, Paths: []string{"web/db/schema.rb"}}); d.Rule != "team-guard" {
		t.Errorf("a lane's copy through its link: %+v", d)
	}
	if d := Decide(lane, Call{Tool: Read, Cwd: f.lane, Paths: []string{"web/db/schema.rb"}}); d.Verdict != Allow {
		t.Errorf("reading a guarded file: %+v", d)
	}
}

// TestAssignmentOnly is the table from the 0.1.0-rc.2 dogfood: a statement that is only an assignment
// has no command word, which two rules once sliced past, so the guard refused every such call.
func TestAssignmentOnly(t *testing.T) {
	f := newFixture(t)
	for _, role := range []Role{Unattached, Lane, Pair} {
		s, cwd := f.scope(role), f.owner
		if role == Lane {
			cwd = f.lane
		}
		run(t, s, cwd, []tcase{
			{`d=x; echo "$d"`, Allow, ""},
			{`foo=1; echo "$foo"`, Allow, ""},
			{`n=a; d=x/$n; echo "$d"`, Allow, ""},
			{`for n in a b; do d=1; done`, Allow, ""},
			{`for n in a b; do d=$n; done`, Allow, ""},
			{`FOO=1 echo hi`, Allow, ""},
			{`echo @{u}`, Allow, ""},
			{`echo a | awk '{print $1}'`, Allow, ""},
			{`for n in a b; do echo "=== $n"; done`, Allow, ""},
			{`for d in a b; do n=$(basename "$d"); echo "$n"; done`, Allow, ""},
		})
	}
	// An assignment is still judged by what it runs.
	run(t, f.scope(Unattached), f.owner, []tcase{{`d=$(git branch -D main)`, Deny, "git"}})
}

func TestUnattachedClassification(t *testing.T) {
	f := newFixture(t)
	s := f.scope(Unattached)
	s.AskSupported = true
	notes := quote(filepath.Join(f.home, "notes.md"))
	run(t, s, f.owner, []tcase{
		{"git -C api worktree list", Allow, ""},
		{"git -C api branch --list 'aw/*'", Allow, ""},
		{"git -C api log --oneline -3", Allow, ""},
		{"git -C api status", Allow, ""},
		{"git branch --list -D x", Deny, "git"},
		{"curl -sI https://example.com/graphql", Allow, ""},
		{"curl -fsSL -H 'Accept: application/json' https://example.com", Allow, ""},
		{"curl -sI -m 5 https://example.com", Allow, ""},
		// Writes a file, sends a body, or reads one: the human decides.
		{"curl -o out https://example.com", Ask, "attach"},
		{"curl -d x=1 https://example.com", Ask, "attach"},
		{"curl -H @headers https://example.com", Ask, "attach"},
		{"curl -sw '%output{x}' https://example.com", Ask, "attach"},
		{"curl file:///etc/hostname", Ask, "attach"},
		{"git log --output=x", Ask, "attach"},
		{"python3 -I -c 'print(1)'", Ask, "attach"},
		{"npm test", Ask, "attach"},
		{"perl -i -pe s/a/b/ " + notes, Ask, "attach"},
		// A visible change to the owner checkout is refused, with the way forward.
		{"rm AGENTS.md", Deny, "attach"},
		{"echo hi > notes.txt", Deny, "attach"},
		{"cp " + notes + " x.md", Deny, "attach"},
		{"sed -i s/a/b/ AGENTS.md", Deny, "attach"},
		// Outside the workspace is not the owner checkout.
		{"echo hi > " + notes, Allow, ""},
		{"echo hi >> ~/other.md", Allow, ""},
		{"rm " + notes, Allow, ""},
		{"cp AGENTS.md " + notes, Allow, ""},
		{"mkdir -p /tmp/aw-x", Allow, ""},
		{"rm $x", Ask, "attach"},
	})
	for _, c := range []struct {
		path string
		want Verdict
	}{{filepath.Join(f.home, "vault", "note.md"), Allow}, {filepath.Join(f.owner, "note.md"), Deny}} {
		if d := Decide(s, Call{Tool: Write, Cwd: f.owner, Paths: []string{c.path}}); d.Verdict != c.want {
			t.Errorf("write %s: got %s (%s)", c.path, d.Verdict, d.Message)
		}
	}
	// A harness that cannot ask gets the refusal, which names the fix.
	s.AskSupported = false
	if d := Decide(s, Call{Tool: Shell, Cwd: f.owner, Command: "npm test"}); d.Verdict != Deny || !strings.Contains(d.Message, "bin/aw attach") {
		t.Errorf("no ask: got %s (%s)", d.Verdict, d.Message)
	}
}

// A lane may not make branches, and the ref hook refuses only after Git has already moved the
// files, so the guard refuses first.
func TestLaneNewBranchRefusedBeforeGitRuns(t *testing.T) {
	f := newFixture(t)
	run(t, f.scope(Lane), f.lane, []tcase{
		{"git switch -c other origin/main", Deny, "git"},
		{"git switch --create other", Deny, "git"},
		{"git checkout -b other", Deny, "git"},
		{"git checkout -B other", Deny, "git"},
		{"git switch --orphan other", Deny, "git"},
		{"git switch aw/own", Allow, ""},
		{"git checkout -- file", Allow, ""},
	})
	s := f.scope(Pair)
	run(t, s, f.owner, []tcase{{"git switch -c other", Allow, ""}})
}

// A message is prose, so a slash in it is not a path, while a path argument is still checked.
func TestMessagesAreNotPaths(t *testing.T) {
	f := newFixture(t)
	run(t, f.scope(Lane), f.lane, []tcase{
		{`git commit -m "Record 2WD/4WD/AWD ../../x"`, Allow, ""},
		{`git commit --message="fix ../../x"`, Allow, ""},
		{`git commit -m "uses $HOME/x"`, Allow, ""},
		{`glab mr note --message "see ../../x"`, Deny, "publication"}, // past the path check, to the policy
		{`git commit -F ../../msg`, Deny, "boundary"},
		{`git -C ../.. commit -m ok`, Deny, "boundary"},
		{`cat -m ../../x`, Deny, "boundary"},
	})
}
