package policy

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
)

// dirOptions take a directory as their value, with or without a slash in it.
var dirOptions = map[string]bool{
	"-C": true, "--git-dir": true, "--work-tree": true, "--prefix": true, "--directory": true,
	"--cwd": true, "--chdir": true,
}

// pathsRule is the path boundary for a shell command (R58): every literal path, redirect target and
// directory option must stay in the lane, and one whose value cannot be known fails closed.
func pathsRule(s Scope, c shell.Command) Decision {
	if s.Role != Lane {
		return allow()
	}
	if !c.Dir.Known {
		return deny("boundary", "this command runs after a cd to a directory aw cannot determine, so it is refused → cd to a literal path inside the lane, %s", quote(s.LaneRoot))
	}
	// Each command must run in the lane: from the owner checkout, a session starts with cd into it.
	// cd is that step (its target is checked below), and attaching or asking who it is is allowed.
	if s.fromOutsideLane(c.Dir.Physical) && len(c.Args) > 0 && c.Name() != "cd" && !isIdentityCommand(c) {
		return deny("boundary", "this session belongs to lane %s, but this command would run in %s → start it with cd %s &&", s.LaneName, quote(c.Dir.Logical), quote(s.LaneRoot))
	}
	cdTarget := false
	check := func(w shell.Word, always bool) Decision {
		text := w.Text
		if !always {
			if i := strings.Index(text, "="); i > 0 && !strings.HasPrefix(text, "/") {
				text = text[i+1:] // --out=../x, NAME=/path
			}
			if !looksLikePath(text) {
				return allow()
			}
		}
		if !w.Known {
			return deny("boundary", "the path %s depends on a value aw cannot determine, so it is refused → write the path literally", quote(w.Text))
		}
		if harmless[text] {
			return allow()
		}
		abs, ok := absolute(text, c.Dir)
		if cdTarget {
			abs = c.Dir.Cd(text).Physical // cd follows links logically, as the shell does
		}
		if !ok {
			return deny("boundary", "the path %s is relative to a directory aw cannot determine → use an absolute path inside the lane", quote(text))
		}
		if s.isSecret(abs) {
			return allow() // the secrets rule decides, with its own message
		}
		return s.boundary(abs, text)
	}
	for _, a := range c.Assigns {
		if d := check(a, false); d.Verdict != Allow {
			return d
		}
	}
	name := c.Name()
	for i, a := range c.Args {
		if i == 0 && isAW(c) {
			continue // running the workspace's launcher touches none of the owner's files
		}
		always := i > 0 && (name == "cd" || name == "pushd") && !strings.HasPrefix(a.Text, "-")
		cdTarget = always
		if i > 0 && c.Args[i-1].Known && dirOptions[c.Args[i-1].Text] {
			always = true
		}
		if i > 0 && strings.HasPrefix(a.Text, "--") {
			if opt, val, ok := strings.Cut(a.Text, "="); ok && dirOptions[opt] {
				if d := check(shell.Word{Text: val, Known: a.Known}, true); d.Verdict != Allow {
					return d
				}
				continue
			}
		}
		if d := check(a, always); d.Verdict != Allow {
			return d
		}
	}
	cdTarget = false
	for _, r := range c.Redirs {
		if isDup(r) || isHeredoc(r) {
			continue
		}
		if d := check(r.Target, true); d.Verdict != Allow {
			return d
		}
	}
	return allow()
}

func isDup(r shell.Redirect) bool {
	return (r.Op == ">&" || r.Op == "<&") && (r.Target.Text == "-" || isDigits(r.Target.Text))
}

func isHeredoc(r shell.Redirect) bool { return strings.HasPrefix(r.Op, "<<") }

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// isAW reports whether a command runs aw: aw on PATH, or a workspace's bin/aw.
func isAW(c shell.Command) bool {
	if len(c.Args) == 0 || !c.Args[0].Known {
		return false
	}
	a := c.Args[0].Text
	return a == "aw" || a == "bin/aw" || strings.HasSuffix(a, "/bin/aw")
}

// isIdentityCommand is aw attach or aw whoami, which a session may run before it is in its lane.
func isIdentityCommand(c shell.Command) bool {
	if !isAW(c) {
		return false
	}
	sub, _ := subcommand(c.Args[1:])
	return sub == "attach" || sub == "whoami"
}

// subcommand is the first argument that is not an option.
func subcommand(args []shell.Word) (string, []shell.Word) {
	for i, a := range args {
		if a.Known && !strings.HasPrefix(a.Text, "-") {
			return a.Text, args[i+1:]
		}
	}
	return "", nil
}

func hasArg(args []shell.Word, names ...string) bool {
	return slices.ContainsFunc(args, func(a shell.Word) bool {
		for _, n := range names {
			if a.Text == n || strings.HasPrefix(a.Text, n+"=") {
				return true
			}
		}
		return false
	})
}

// hasShortFlag reports whether a bundled short option such as -fd contains the letter.
func hasShortFlag(args []shell.Word, letter byte) bool {
	return slices.ContainsFunc(args, func(a shell.Word) bool {
		t := a.Text
		return len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.IndexByte(t[1:], letter) >= 0
	})
}

// ownerOnly are the aw commands only the human runs (ux.md, "Commands").
var ownerOnly = map[string]string{
	"new": "starts a session", "resume": "reopens sessions", "open": "starts a session",
	"park": "shelves a lane", "unpark": "restores a lane", "discard": "abandons a lane's work",
	"pair": "grants a pair session", "move-to-lane": "moves the owner's work", "try": "changes the owner checkout",
	"setup": "sets up the workspace", "rehearse": "rehearses onboarding", "delegate": "grants authority",
	"upgrade": "changes the pinned version", "init": "creates a workspace",
}

// OwnerOnly reports whether an aw command is one only the human runs.
func OwnerOnly(command string) bool {
	_, ok := ownerOnly[command]
	return ok
}

// ownerOnlyRule refuses the human's aw commands to agents, wherever they appear: in a pipe, a
// substitution or a wrapper (R60, R64).
func ownerOnlyRule(s Scope, c shell.Command) Decision {
	if !isAW(c) {
		return allow()
	}
	sub, rest := subcommand(c.Args[1:])
	where := ""
	if c.Nested {
		where = " (inside a substitution)"
	}
	if sub == "setup" && ownLaneSetup(s, rest) {
		return allow()
	}
	if why, ok := ownerOnly[sub]; ok {
		return deny("owner-only", "aw %s %s, which only the human does%s → ask the human to run it", sub, why, where)
	}
	if sub == "sweep" && (hasArg(rest, "--apply") || hasArg(rest, "--retry")) {
		return deny("owner-only", "aw sweep --apply removes lanes, which only the human does%s → aw sweep, without --apply, shows what it would do", where)
	}
	return allow()
}

// ownLaneSetup reports whether this is a lane agent retrying its own lane's setup: its hooks are the
// team's reviewed configuration, and a failed step's error tells the agent to run this. A lane is
// named exactly here. aw checks the lane again when the command runs.
func ownLaneSetup(s Scope, rest []shell.Word) bool {
	if s.LaneRoot == "" || len(rest) != 1 || !rest[0].Known {
		return false
	}
	return rest[0].Text == s.LaneName || rest[0].Text == filepath.Base(s.LaneRoot)
}

// gitGlobal splits git's global options from the subcommand.
func gitGlobal(args []shell.Word) (config []string, sub string, rest []shell.Word, unknown bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.Known {
			return config, "", nil, true
		}
		switch {
		case a.Text == "-c" && i+1 < len(args):
			config = append(config, args[i+1].Text)
			i++
		case strings.HasPrefix(a.Text, "-c") && len(a.Text) > 2:
			config = append(config, a.Text[2:])
		case a.Text == "--config-env" && i+1 < len(args):
			config = append(config, args[i+1].Text)
			i++
		case a.Text == "-C" || a.Text == "--git-dir" || a.Text == "--work-tree" || a.Text == "--namespace" || a.Text == "--exec-path":
			i++
		case strings.HasPrefix(a.Text, "-"):
		default:
			return config, a.Text, args[i+1:], false
		}
	}
	return config, "", nil, false
}

// gitRule refuses destructive Git and changes to what the owner shares with the lanes: shared
// configuration, hooks and worktrees (owner-checkout.md).
func gitRule(s Scope, c shell.Command) Decision {
	if c.Name() != "git" {
		return allow()
	}
	config, sub, args, unknown := gitGlobal(c.Args[1:])
	if unknown {
		return deny("git", "this git command has an option aw cannot determine, so it is refused → write its options literally")
	}
	for _, kv := range config {
		key, _, _ := strings.Cut(strings.ToLower(kv), "=")
		if key == "core.hookspath" || key == "core.fsmonitor" || key == "core.sshcommand" || strings.HasPrefix(key, "alias.") {
			return deny("git", "git -c %s changes how Git runs hooks or commands, which bypasses the workspace's protections → run git without it", key)
		}
	}
	destructive := func(what string) Decision {
		return deny("git", "git %s %s, which agents do not do → ask the human, or leave it for review", sub, what)
	}
	switch sub {
	case "push":
		for _, a := range args {
			t := a.Text
			switch {
			case t == "-f" || t == "--force" || strings.HasPrefix(t, "--force-with-lease") || t == "--force-if-includes" || t == "--mirror":
				return destructive("rewrites the remote's history")
			case t == "-d" || t == "--delete" || t == "--prune":
				return destructive("deletes remote branches")
			case strings.HasPrefix(t, "+"):
				return destructive("force-pushes a refspec")
			case strings.HasPrefix(t, ":") && len(t) > 1:
				return destructive("deletes a remote branch")
			}
		}
		if hasShortFlag(args, 'f') || hasShortFlag(args, 'd') {
			return destructive("forces or deletes")
		}
	case "branch":
		if hasArg(args, "--delete", "--move", "--copy", "--force", "--set-upstream-to", "--unset-upstream", "--edit-description") ||
			hasShortFlag(args, 'd') || hasShortFlag(args, 'D') || hasShortFlag(args, 'm') || hasShortFlag(args, 'M') ||
			hasShortFlag(args, 'c') || hasShortFlag(args, 'C') || hasShortFlag(args, 'f') {
			return destructive("deletes, moves or forces a branch")
		}
	case "tag":
		if hasArg(args, "--delete", "--force") || hasShortFlag(args, 'd') || hasShortFlag(args, 'f') {
			return destructive("deletes or moves a tag")
		}
	case "reset":
		if hasArg(args, "--hard", "--merge", "--keep") {
			return destructive("discards uncommitted work")
		}
	case "clean":
		if hasArg(args, "--force") || hasShortFlag(args, 'f') {
			return destructive("deletes untracked files")
		}
	case "checkout", "switch":
		if hasArg(args, "--force", "--discard-changes") || hasShortFlag(args, 'f') {
			return destructive("discards uncommitted work")
		}
	case "stash":
		if st, _ := subcommand(args); st == "drop" || st == "clear" {
			return destructive("deletes stashed work")
		}
	case "update-ref", "symbolic-ref":
		if sub == "update-ref" || len(nonOptions(args)) > 1 || hasArg(args, "--delete") || hasShortFlag(args, 'd') {
			return destructive("writes refs directly")
		}
	case "reflog":
		if st, _ := subcommand(args); st == "expire" || st == "delete" {
			return destructive("deletes history")
		}
	case "filter-branch", "filter-repo", "replace":
		return destructive("rewrites history")
	case "worktree":
		if st, _ := subcommand(args); st != "list" {
			return deny("git", "git worktree %s changes the workspace's worktrees, which aw manages → use aw attach for a lane", st)
		}
	case "config":
		if gitConfigWrites(args) {
			return deny("git", "git config writes shared Git configuration, which the owner checkout and every lane use → ask the human")
		}
	case "remote":
		if st, _ := subcommand(args); st != "" && st != "show" && st != "get-url" {
			return deny("git", "git remote %s changes shared Git configuration → ask the human", st)
		}
	case "gc", "prune", "repack":
		if hasArg(args, "--prune") || sub == "prune" {
			return destructive("deletes unreachable objects, which may hold other lanes' work")
		}
	}
	return allow()
}

func nonOptions(args []shell.Word) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a.Text, "-") {
			out = append(out, a.Text)
		}
	}
	return out
}

// gitConfigWrites reports whether git config writes. Reads: --get*, --list, -l, the get and list
// subcommands, and a lone key. Worktree-local writes (--worktree) are a lane's own.
func gitConfigWrites(args []shell.Word) bool {
	if hasArg(args, "--worktree") {
		return false
	}
	if hasArg(args, "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--get-color", "--get-colorbool", "--list", "-l", "--show-origin", "--show-scope", "--name-only") {
		return hasArg(args, "--add", "--unset", "--unset-all", "--replace-all", "--rename-section", "--remove-section", "--edit", "-e")
	}
	vals := nonOptions(args)
	if len(vals) > 0 && (vals[0] == "get" || vals[0] == "list") {
		return false
	}
	if len(vals) > 0 && slices.Contains([]string{"set", "unset", "rename-section", "remove-section", "edit"}, vals[0]) {
		return true
	}
	return len(vals) != 1 || hasArg(args, "--add", "--unset", "--unset-all", "--replace-all", "--rename-section", "--remove-section", "--edit", "-e")
}

var containerChanging = map[string]bool{
	"run": true, "create": true, "start": true, "stop": true, "restart": true, "rm": true, "kill": true,
	"pause": true, "unpause": true, "rename": true, "update": true, "up": true, "down": true, "build": true,
	"prune": true, "rmi": true, "commit": true, "attach": false,
}

// containerRule refuses creating, restarting or stopping containers (F5, R49): they are shared,
// and their ports and mounts are fixed when they are created.
func containerRule(s Scope, c shell.Command) Decision {
	name := c.Name()
	args := c.Args[1:]
	refuse := func(what string) Decision {
		return deny("containers", "%s %s changes shared containers, which agents do not do → aw run runs a command in the lane's container; ask the human for anything else", name, what)
	}
	switch name {
	case "docker", "podman", "nerdctl":
		sub, rest := subcommand(args)
		switch sub {
		case "compose":
			if cs, _ := subcommand(rest); containerChanging[cs] || cs == "create" {
				return refuse("compose " + cs)
			}
		case "container", "volume", "network", "system", "image":
			if cs, _ := subcommand(rest); containerChanging[cs] || cs == "remove" || cs == "connect" || cs == "disconnect" {
				return refuse(sub + " " + cs)
			}
		default:
			if containerChanging[sub] {
				return refuse(sub)
			}
		}
	case "docker-compose", "podman-compose":
		if cs, _ := subcommand(args); containerChanging[cs] {
			return refuse(cs)
		}
	case "devcontainer":
		if cs, _ := subcommand(args); cs == "up" || cs == "build" || cs == "set-up" || cs == "run-user-commands" {
			return refuse(cs)
		}
	}
	return allow()
}

// killRule refuses stopping processes directly (R58): the owner's processes are on the same
// machine, and servers belong to aw serve and aw stop.
func killRule(s Scope, c shell.Command) Decision {
	name := c.Name()
	args := c.Args[1:]
	refuse := func() Decision {
		return deny("processes", "%s stops processes, which agents do not do: the owner's processes run on the same machine → aw stop <resource> stops a server this lane holds", name)
	}
	switch name {
	case "kill", "pkill", "killall", "skill", "slay", "taskkill":
		if name == "kill" && hasArg(args, "-l", "-L") {
			return allow()
		}
		return refuse()
	case "fuser":
		if hasShortFlag(args, 'k') || hasArg(args, "--kill") {
			return refuse()
		}
	case "systemctl", "launchctl", "service":
		if sub, _ := subcommand(args); slices.Contains([]string{"stop", "restart", "kill", "bootout", "unload", "remove", "disable", "mask", "reload"}, sub) {
			return refuse()
		}
	}
	return allow()
}

// serverRule refuses starting a configured server directly: aw serve takes a seat for it.
func serverRule(s Scope, c shell.Command) Decision {
	if len(c.Args) == 0 {
		return allow()
	}
	words := make([]string, 0, len(c.Args))
	for _, a := range c.Args {
		words = append(words, a.Text)
	}
	for _, sv := range s.Servers {
		// The command itself: up to its first option or placeholder, since the agent's flags and port
		// will differ from the configured ones.
		var pattern []string
		for _, w := range slices.DeleteFunc(slices.Clone(sv.Words), func(w string) bool { return w == "exec" }) {
			if strings.HasPrefix(w, "-") || strings.Contains(w, "{") {
				break
			}
			pattern = append(pattern, w)
		}
		if len(pattern) == 0 || len(words) < len(pattern) {
			continue
		}
		match := true
		for i, p := range pattern {
			if words[i] != p && filepath.Base(words[i]) != filepath.Base(p) {
				match = false
				break
			}
		}
		if match {
			project, server, _ := strings.Cut(sv.Name, ".")
			return deny("servers", "this starts the %s server of %s directly; servers take a seat through aw → bin/aw serve <resource> (the resource that runs %s.%s)", server, project, project, server)
		}
	}
	return allow()
}

var procEnviron = regexp.MustCompile(`^/proc/(self|thread-self|[0-9]+)(/task/[0-9]+)?/environ$`)

// secretRule refuses reading or echoing secrets (R68, F21): the secret files, the environment,
// and a process's environ.
func secretRule(s Scope, c shell.Command) Decision {
	name := c.Name()
	args := c.Args
	if len(args) > 0 {
		args = args[1:]
	}
	echo := func(what string) Decision {
		return deny("secrets", "%s prints the environment, which holds secrets → print one variable you need, such as echo \"$PATH\", or let the tool read its own configuration", what)
	}
	switch name {
	case "env", "printenv":
		if len(args) == 0 || name == "printenv" || (len(args) > 0 && args[0].Known && strings.HasPrefix(args[0].Text, "-") && len(nonOptions(args)) == 0) {
			return echo(name)
		}
	case "set", "export", "declare", "typeset":
		if len(args) == 0 || (len(args) == 1 && (args[0].Text == "-p" || args[0].Text == "-x" || args[0].Text == "-px")) {
			return echo(name)
		}
	}
	mentions := func(w shell.Word) (string, bool) {
		if !w.Known {
			return "", false
		}
		t := w.Text
		if i := strings.Index(t, "="); i > 0 && !strings.HasPrefix(t, "/") {
			t = t[i+1:]
		}
		if procEnviron.MatchString(t) {
			return t, true
		}
		if t == "" || strings.HasPrefix(t, "-") || len(s.Secrets) == 0 {
			return "", false
		}
		abs, ok := absolute(t, c.Dir)
		return t, ok && s.isSecret(abs)
	}
	for _, a := range args {
		if t, ok := mentions(a); ok {
			return deny("secrets", "this command reads %s, which holds secrets → tools that need it read it themselves; to check a value is set, ask the human", quote(t))
		}
	}
	for _, r := range c.Redirs {
		if t, ok := mentions(r.Target); ok {
			return deny("secrets", "this command reads or writes %s, which holds secrets → leave the file to the tools that use it", quote(t))
		}
	}
	return allow()
}

// readOnly are commands that only read, for a session that is not attached to a lane. git, aw and a
// few others are decided by their subcommand.
var readOnly = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "less": true, "more": true, "wc": true, "grep": true,
	"egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true, "tree": true, "file": true, "stat": true,
	"du": true, "df": true, "pwd": true, "echo": true, "printf": true, "true": true, "false": true, "test": true,
	"[": true, "which": true, "type": true, "whoami": true, "id": true, "date": true, "uname": true,
	"hostname": true, "jq": true, "yq": true, "sort": true, "uniq": true, "cut": true, "tr": true, "diff": true,
	"cmp": true, "comm": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
	"sha256sum": true, "sha1sum": true, "md5sum": true, "shasum": true, "cksum": true, "nl": true, "column": true,
	"fold": true, "fmt": true, "rev": true, "tac": true, "od": true, "xxd": true, "hexdump": true, "strings": true,
	"bat": true, "fd": true, "fdfind": true, "locate": true, "ps": true, "top": false, "uptime": true,
	"sleep": true, "seq": true, "expr": true, "cd": true, "pushd": true, "popd": true, "exit": true,
	"command": true, "hash": true, "help": true, "man": true, "awk": true, "sed": true, "find": true,
	"lsof": true, "free": true, "nproc": true, "getconf": true, "locale": true, "tput": true, "xargs": true,
}

var gitReadOnly = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "blame": true, "grep": true, "ls-files": true,
	"ls-tree": true, "rev-parse": true, "rev-list": true, "describe": true, "shortlog": true, "cat-file": true,
	"for-each-ref": true, "show-ref": true, "merge-base": true, "name-rev": true, "whatchanged": true,
	"cherry": true, "range-diff": true, "version": true, "help": true, "check-ignore": true, "check-attr": true,
	"var": true, "count-objects": true, "show-branch": true, "annotate": true, "": true,
}

// isReadOnly reports whether a command only reads.
func isReadOnly(c shell.Command) bool {
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) && !harmless[r.Target.Text] {
			return false
		}
	}
	if len(c.Args) == 0 {
		return true
	}
	name := c.Name()
	args := c.Args[1:]
	switch name {
	case "git":
		_, sub, rest, unknown := gitGlobal(args)
		if unknown {
			return false
		}
		switch sub {
		case "branch":
			return len(nonOptions(rest)) == 0 && !hasShortFlag(rest, 'd') && !hasShortFlag(rest, 'D') && !hasShortFlag(rest, 'm') && !hasShortFlag(rest, 'M') && !hasShortFlag(rest, 'c') && !hasShortFlag(rest, 'C')
		case "tag":
			return len(nonOptions(rest)) == 0 || hasArg(rest, "-l", "--list")
		case "stash":
			st, _ := subcommand(rest)
			return st == "list" || st == "show"
		case "remote":
			st, _ := subcommand(rest)
			return st == "" || st == "show" || st == "get-url"
		case "config":
			return !gitConfigWrites(rest)
		case "worktree":
			st, _ := subcommand(rest)
			return st == "list"
		case "reflog":
			st, _ := subcommand(rest)
			return st == "" || st == "show"
		}
		return gitReadOnly[sub]
	case "sed":
		return !hasArg(args, "--in-place") && !hasShortFlag(args, 'i')
	case "awk", "gawk":
		return !slices.ContainsFunc(args, func(a shell.Word) bool { return strings.Contains(a.Text, "inplace") })
	case "find":
		return !hasArg(args, "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls")
	}
	return readOnly[name]
}

// herdrReads are the herdr verbs that only look, or that a harness uses to report its own state.
var herdrReads = map[string]bool{
	"list": true, "current": true, "get": true, "layout": true, "read": true, "process-info": true, "neighbor": true,
	"edges": true, "wait-output": true, "report-agent": true, "report-agent-session": true, "release-agent": true,
	"report-metadata": true, "status": true, "snapshot": true, "schema": true,
}

// muxRule stops a lane agent from using a multiplexer to run something elsewhere (R58): a pane it
// opens or types into runs outside the lane and outside these guards. Looking is allowed.
func muxRule(s Scope, c shell.Command) Decision {
	if s.Role != Lane || len(c.Args) == 0 {
		return allow()
	}
	name := c.Name()
	args := c.Args[1:]
	refuse := func() Decision {
		return deny("multiplexer", "%s would run a command in a pane outside this lane, where the guards cannot see it → run it here, in the lane's shell", name)
	}
	for _, a := range args {
		if a.Known && strings.Contains(a.Text, "herdr") && strings.Contains(a.Text, ".sock") {
			return deny("multiplexer", "this command talks to herdr's socket, which can run commands outside this lane → run it here, in the lane's shell")
		}
	}
	switch name {
	case "tmux":
		for i := 0; i < len(args); i++ {
			t := args[i].Text
			if !args[i].Known {
				return refuse()
			}
			if t == "-L" || t == "-S" || t == "-f" || t == "-T" {
				i++
				continue
			}
			if strings.HasPrefix(t, "-") {
				continue
			}
			if strings.HasPrefix(t, "list-") || strings.HasPrefix(t, "show-") || t == "ls" || t == "lsw" || t == "lsp" ||
				t == "has-session" || t == "has" || t == "display-message" || t == "display" || t == "capture-pane" || t == "capturep" {
				return allow()
			}
			return refuse()
		}
		return refuse()
	case "herdr":
		var words []string
		for i := 0; i < len(args); i++ {
			t := args[i].Text
			if !args[i].Known {
				return refuse()
			}
			if t == "--session" || t == "--machine" || t == "--remote" {
				i++
				continue
			}
			if !strings.HasPrefix(t, "-") {
				words = append(words, t)
			}
		}
		if len(words) > 1 && herdrReads[words[1]] || len(words) == 1 && words[0] == "status" {
			return allow()
		}
		return refuse()
	case "zellij":
		if sub, _ := subcommand(args); sub == "list-sessions" || sub == "ls" {
			return allow()
		}
		return refuse()
	case "screen":
		if hasArg(args, "-ls", "-list") {
			return allow()
		}
		return refuse()
	}
	if s.MuxProgram != "" && name == s.MuxProgram {
		return refuse()
	}
	return allow()
}

// writeTargets are the paths a command writes, removes or changes, for the common commands that do
// so. Like every shell rule it stops accidents by cooperative agents: a program that writes files
// by its own logic is not visible here.
func writeTargets(c shell.Command) []shell.Word {
	if len(c.Args) == 0 {
		return nil
	}
	args := c.Args[1:]
	plain := slices.DeleteFunc(slices.Clone(args), func(a shell.Word) bool { return a.Known && strings.HasPrefix(a.Text, "-") })
	switch c.Name() {
	case "rm", "mv", "tee", "touch", "chmod", "chown", "chgrp", "mkdir", "rmdir", "truncate", "unlink", "shred", "patch":
		return plain
	case "cp", "install", "ln", "rsync":
		for i, a := range args[:max(len(args)-1, 0)] {
			if a.Text == "-t" || a.Text == "--target-directory" {
				return []shell.Word{args[i+1]}
			}
		}
		if len(plain) > 0 {
			return plain[len(plain)-1:]
		}
	case "dd":
		var out []shell.Word
		for _, a := range args {
			if v, ok := strings.CutPrefix(a.Text, "of="); ok {
				out = append(out, shell.Word{Text: v, Known: a.Known})
			}
		}
		return out
	case "sed", "perl", "ruby":
		if hasArg(args, "--in-place") || hasShortFlag(args, 'i') {
			return plain
		}
	}
	return nil
}

// selfModRule applies the rule on a pair session's file tools to its shell commands (R63, R60):
// redirects, the common file commands, and any writing command that names aw's own records.
func selfModRule(s Scope, c shell.Command) Decision {
	if s.Role != Pair {
		return allow()
	}
	first := allow()
	check := func(w shell.Word) Decision {
		abs, ok := "", false
		if w.Known {
			abs, ok = absolute(w.Text, c.Dir)
		}
		if !ok {
			return ask("self-modification", "this command writes to %s, and aw cannot tell whether that is the workspace's own configuration → write the path literally, or ask the human to approve it", quote(w.Text))
		}
		return s.selfModification(abs, w.Text)
	}
	targets := writeTargets(c)
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) && !harmless[r.Target.Text] {
			targets = append(targets, r.Target)
		}
	}
	for _, t := range targets {
		switch d := check(t); d.Verdict {
		case Deny:
			return d
		case Ask:
			if first.Verdict == Allow {
				first = d
			}
		}
	}
	if !isReadOnly(c) && len(c.Args) > 1 && !isAW(c) {
		for _, a := range c.Args[1:] {
			if abs, ok := absolute(a.Text, c.Dir); a.Known && ok && within(abs, filepath.Join(s.Owner, ".git", "aw")) {
				return deny("self-modification", "%s is aw's own record of this workspace, which agents do not change → ask the human", quote(a.Text))
			}
		}
	}
	return first
}

// unattachedRule claims lanes lazily: a session in the owner checkout with no lane may read, and
// anything else is refused with the attach and pair instructions.
func unattachedRule(s Scope, c shell.Command) Decision {
	if s.Role != Unattached || isAW(c) || isReadOnly(c) {
		return allow()
	}
	what := "this command"
	if n := c.Name(); n != "" {
		what = n
	}
	return deny("attach", "%s may change files, and this session is not attached to a lane: the owner checkout belongs to the human %s", what, s.attachHint())
}

// publicationRule asks before anything outward-facing (R72, its default): pushing, opening or
// merging changes, publishing packages and images.
func publicationRule(s Scope, c shell.Command) Decision {
	name := c.Name()
	args := c.Args
	if len(args) > 0 {
		args = args[1:]
	}
	sub, rest := subcommand(args)
	sub2, _ := subcommand(rest)
	what := ""
	switch name {
	case "git":
		if _, gs, _, _ := gitGlobal(args); gs == "push" {
			what = "git push publishes commits to the remote"
		}
	case "gh":
		switch {
		case sub == "pr" && slices.Contains([]string{"create", "merge", "close", "reopen", "comment", "review", "edit", "ready"}, sub2),
			sub == "issue" && slices.Contains([]string{"create", "close", "comment", "edit", "delete", "reopen"}, sub2),
			sub == "release" && slices.Contains([]string{"create", "delete", "edit", "upload"}, sub2),
			sub == "repo" && slices.Contains([]string{"create", "delete", "edit", "rename", "archive", "fork"}, sub2),
			sub == "workflow" && slices.Contains([]string{"run", "enable", "disable"}, sub2),
			sub == "api" && slices.ContainsFunc(rest, func(a shell.Word) bool {
				return a.Text == "-X" || a.Text == "--method" || strings.HasPrefix(a.Text, "-f") || strings.HasPrefix(a.Text, "-F") || a.Text == "--input"
			}):
			what = "gh " + sub + " " + sub2 + " acts on GitHub"
		}
	case "glab":
		switch {
		case sub == "mr" && slices.Contains([]string{"create", "merge", "close", "reopen", "note", "approve", "update"}, sub2),
			sub == "issue" && slices.Contains([]string{"create", "close", "note", "update", "delete"}, sub2),
			sub == "release" && slices.Contains([]string{"create", "delete", "upload"}, sub2),
			sub == "repo" && slices.Contains([]string{"create", "delete", "fork", "archive"}, sub2),
			sub == "api" && slices.ContainsFunc(rest, func(a shell.Word) bool { return a.Text == "-X" || a.Text == "--method" }):
			what = "glab " + sub + " " + sub2 + " acts on GitLab"
		}
	case "npm", "pnpm", "yarn":
		if sub == "publish" || sub == "unpublish" || sub == "deprecate" {
			what = name + " " + sub + " publishes a package"
		}
	case "docker", "podman":
		if sub == "push" || (sub == "image" && sub2 == "push") {
			what = name + " push publishes an image"
		}
	case "cargo", "gem", "twine", "poetry", "flit", "hatch":
		if sub == "publish" || sub == "push" || sub == "upload" || sub == "yank" {
			what = name + " " + sub + " publishes a package"
		}
	}
	if what == "" {
		return allow()
	}
	return ask("publication", "%s, which needs the human's approval (the publication policy) → ask the human to approve it", what)
}
