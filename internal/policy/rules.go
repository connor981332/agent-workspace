package policy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/shell"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// dirOptions take a directory as their value, with or without a slash in it.
var dirOptions = map[string]bool{
	"-C": true, "--git-dir": true, "--work-tree": true, "--prefix": true, "--directory": true,
	"--cwd": true, "--chdir": true,
}

// fileChanging are the commands whose arguments are files they change, so an argument aw cannot
// determine could be anything.
var fileChanging = map[string]bool{"rm": true, "rmdir": true, "mv": true, "cp": true, "ln": true, "tee": true, "chmod": true, "chown": true,
	"touch": true, "mkdir": true, "truncate": true, "shred": true, "install": true}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

// computedRule fails closed on what cannot be read (R55): a command whose name is computed, a
// command that changes files named by a value only known when it runs, and commands fed to a shell
// on its standard input.
func computedRule(s Scope, c shell.Command) Decision {
	if len(c.Args) == 0 || (s.Role == Pair && !s.mustRead()) {
		return allow()
	}
	if !c.Args[0].Known {
		return deny("computed", "the name of this command depends on a value aw cannot determine, so it is refused → write the command itself")
	}
	name := c.Name()
	if s.Role == Lane && fileChanging[name] {
		for _, a := range c.Args[1:] {
			if !a.Known {
				return deny("computed", "%s is given a value aw cannot determine, so it could reach outside the lane, and it is refused → write the path out", name)
			}
		}
	}
	if shells[name] && (len(c.Args) == 1 || (len(c.Args) == 2 && c.Args[1].Text == "-s")) {
		return deny("computed", "%s would run whatever is on its standard input, which aw cannot read, so it is refused → save the script to a file in the lane, read it, and run that", name)
	}
	return allow()
}

// pathsRule is the path boundary for a shell command (R58). What a command writes must stay in the
// lane, and a written path whose value cannot be known fails closed. What it only reads may be
// anywhere: reading the owner checkout disturbs nothing, and a read of the owner's copy of a file the
// lane has too comes with a note naming the lane's.
func pathsRule(s Scope, c shell.Command) Decision {
	if s.Role != Lane {
		return allow()
	}
	reads := isReadOnly(c)
	// An unknown path may name a secret, which the secrets safeguard keeps from being read.
	unknownReadOK := !s.safeguard("secrets")
	if !c.Dir.Known && !(reads && unknownReadOK) {
		return deny("boundary", "this command runs after a cd to a directory aw cannot determine, so it is refused → cd to a literal path inside the lane, %s; a variable set earlier in the same command is fine", quote(s.LaneRoot))
	}
	// Each command must run in the lane: from the owner checkout, a session starts with cd into it.
	// cd is that step, and attaching or asking who it is is allowed. A command that only reads may
	// run there when it says so: after its own cd, or naming every path absolutely.
	if c.Dir.Known && s.fromOutsideLane(c.Dir.Physical) && len(c.Args) > 0 && c.Name() != "cd" && !isIdentityCommand(c) &&
		!(reads && (c.Dir.Physical != s.start.Physical || namesPathsAbsolutely(c))) {
		return deny("boundary", "this session belongs to lane %s, but this command would run in %s → start it with cd %s &&", s.LaneName, quote(c.Dir.Logical), quote(s.LaneRoot))
	}
	// A command whose writes are known (cp, mv, redirects…) only reads its other paths.
	targets, understood := unattachedWrites(c)
	written := func(w shell.Word) bool {
		if reads {
			return false
		}
		if !understood || slices.Contains(targets, w) {
			return true
		}
		_, val, ok := strings.Cut(w.Text, "=")
		return ok && slices.Contains(targets, shell.Word{Text: val, Known: w.Known})
	}
	var notes []string
	cdTarget := false
	check := func(w shell.Word, always, write bool) Decision {
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
			if !write && unknownReadOK {
				return allow()
			}
			return deny("boundary", "the path %s depends on a value aw cannot determine, so it is refused → write the path literally; a variable set earlier in the same command, or a for loop over literal words, is fine", quote(w.Text))
		}
		if harmless[text] {
			return allow()
		}
		abs, ok := absolute(text, c.Dir)
		if cdTarget {
			abs = c.Dir.Cd(text).Physical // cd follows links logically, as the shell does
		}
		if !ok {
			if !write && unknownReadOK {
				return allow()
			}
			return deny("boundary", "the path %s is relative to a directory aw cannot determine → use an absolute path inside the lane", quote(text))
		}
		if s.isSecret(abs) {
			return allow() // the secrets rule decides, with its own message
		}
		if !write {
			if n := s.ownerCopyNote(abs, text); n != "" && !slices.Contains(notes, n) {
				notes = append(notes, n)
			}
			return allow()
		}
		return s.boundary(abs, text)
	}
	// An assignment with no command only sets a shell variable, and every use of it is checked. As a
	// prefix it reaches the command's environment, so it is a path that command may use.
	if len(c.Args) > 0 {
		for _, a := range c.Assigns {
			if d := check(a, false, !reads); d.Verdict != Allow {
				return d
			}
		}
	}
	name := c.Name()
	endpoint := apiEndpoint(c)
	for i, a := range c.Args {
		if i == 0 && isAW(c) {
			continue // running the workspace's launcher touches none of the owner's files
		}
		if i == endpoint {
			continue // gh api repos/o/r/pulls: a path on the forge, not a file
		}
		if i > 0 && isMessageText(name, c.Args[i-1], a) {
			continue // a commit message or a title is prose, whatever slashes it has
		}
		always := i > 0 && (name == "cd" || name == "pushd") && !strings.HasPrefix(a.Text, "-")
		cdTarget = always
		if i > 0 && c.Args[i-1].Known && dirOptions[c.Args[i-1].Text] {
			always = true
		}
		if i > 0 && strings.HasPrefix(a.Text, "--") {
			if opt, val, ok := strings.Cut(a.Text, "="); ok && dirOptions[opt] {
				if d := check(shell.Word{Text: val, Known: a.Known}, true, written(a)); d.Verdict != Allow {
					return d
				}
				continue
			}
		}
		if d := check(a, always, written(a)); d.Verdict != Allow {
			return d
		}
	}
	cdTarget = false
	for _, r := range c.Redirs {
		if isDup(r) || isHeredoc(r) {
			continue
		}
		if d := check(r.Target, true, strings.Contains(r.Op, ">")); d.Verdict != Allow {
			return d
		}
	}
	return allowWith(strings.Join(notes, " "))
}

// namesPathsAbsolutely reports whether a command names at least one path, and every path it names
// is absolute, so it means the same wherever it runs.
func namesPathsAbsolutely(c shell.Command) bool {
	n := 0
	for i, a := range c.Args[1:] {
		text := a.Text
		if !looksLikePath(text) && !(i > 0 && dirOptions[c.Args[i].Text]) {
			continue
		}
		if !a.Known || !filepath.IsAbs(text) {
			return false
		}
		n++
	}
	return n > 0
}

// apiValueOptions are the options of gh api and glab api that take a value.
var apiValueOptions = map[string]bool{"-X": true, "--method": true, "-H": true, "--header": true, "-f": true, "--raw-field": true,
	"-F": true, "--field": true, "-q": true, "--jq": true, "-t": true, "--template": true, "--hostname": true, "--input": true,
	"-p": true, "--preview": true, "--cache": true}

// apiEndpoint is the index of the endpoint argument of gh api or glab api, or -1.
func apiEndpoint(c shell.Command) int {
	if n := c.Name(); (n != "gh" && n != "glab") || len(c.Args) < 3 || c.Args[1].Text != "api" {
		return -1
	}
	for i := 2; i < len(c.Args); i++ {
		t := c.Args[i].Text
		switch {
		case apiValueOptions[t]:
			i++
		case strings.HasPrefix(t, "-"):
		default:
			return i
		}
	}
	return -1
}

// messageOptions take prose for git and the forge CLIs. -F and the like name files, so are not here.
var messageOptions = map[string]bool{"-m": true, "--message": true, "--title": true, "-t": true, "--body": true, "-b": true, "--description": true, "-d": true}

// isMessageText reports whether an argument is the text of a message: the value after such an
// option, or the option written with its value as --message=text.
func isMessageText(name string, prev, arg shell.Word) bool {
	if name != "git" && name != "gh" && name != "glab" {
		return false
	}
	if prev.Known && messageOptions[prev.Text] {
		return true
	}
	opt, _, ok := strings.Cut(arg.Text, "=")
	return ok && strings.HasPrefix(opt, "--") && messageOptions[opt]
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

// subcommandAfter is subcommand for a tool whose options can stand before it and take a value in the
// next word, so that the value is not mistaken for the subcommand: docker compose -p shop down.
func subcommandAfter(args []shell.Word, valueOptions map[string]bool) (string, []shell.Word) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.Known {
			continue
		}
		if valueOptions[a.Text] {
			i++
			continue
		}
		if !strings.HasPrefix(a.Text, "-") {
			return a.Text, args[i+1:]
		}
	}
	return "", nil
}

var (
	dockerValueOptions = map[string]bool{"-H": true, "--host": true, "-c": true, "--context": true, "--config": true, "-l": true, "--log-level": true,
		"--tlscacert": true, "--tlscert": true, "--tlskey": true}
	composeValueOptions = map[string]bool{"-p": true, "--project-name": true, "-f": true, "--file": true, "--env-file": true, "--profile": true,
		"--project-directory": true, "--ansi": true, "--parallel": true, "--progress": true}
	forgeValueOptions = map[string]bool{"-R": true, "--repo": true, "--hostname": true}
)

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
	"setup": "sets up the workspace", "onboard": "starts an onboarding session", "rehearse": "rehearses onboarding", "delegate": "grants authority",
	"upgrade": "changes the pinned version", "init": "creates a workspace",
	"containers": "starts, recreates or stops shared containers",
	"infra":      "takes down the shared infrastructure", "supervise": "installs a user service",
	"add": "changes the workspace's modules or projects", "eject": "changes what aw manages",
	"pull": "updates the owner checkouts",
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
	if hasArg(rest, "--help", "-h") {
		return allow() // help changes nothing, and the human's commands document themselves too
	}
	where := ""
	if c.Nested {
		where = " (inside a substitution)"
	}
	if sub == "setup" && ownLaneSetup(s, rest) {
		return allow()
	}
	if sub == "rehearse" && s.LaneRoot != "" && (len(rest) == 0 || ownLaneSetup(s, rest)) {
		return allow() // on a throwaway copy of its own lane; aw checks the lane again when it runs
	}
	if s.Delegated && delegable(sub, rest) {
		return allow()
	}
	if sub == "delegate" && (hasArg(rest, "--status") || hasArg(rest, "--revoke")) {
		return allow() // these never widen anyone's authority
	}
	if sub == "serve" && slices.Contains(s.RoleDeny, "servers") {
		return deny("role", "this lane's role may not hold servers → ask the human, or hand this to a session in another role")
	}
	if sub == "infra" {
		// Starting and reading are safe from any session; taking it down is the human's (R73).
		if cs, _ := subcommand(rest); cs != "down" {
			return allow()
		}
	}
	if sub == "containers" {
		// The claim step only removes a dead leftover, and a harness's devcontainer up runs it.
		if cs, _ := subcommand(rest); cs == "claim" {
			return allow()
		}
	}
	if why, ok := ownerOnly[sub]; ok {
		return ask("owner-only", "aw %s %s, which only the human does%s → approve it if you asked for this", sub, why, where)
	}
	if sub == "sweep" && !s.Delegated && (hasArg(rest, "--apply") || hasArg(rest, "--retry")) {
		return ask("owner-only", "aw sweep --apply removes lanes, which only the human does%s → approve it if you asked for this; aw sweep, without --apply, shows what it would do", where)
	}
	return allow()
}

func wordTexts(rest []shell.Word) []string {
	out := make([]string, 0, len(rest))
	for _, w := range rest {
		if !w.Known {
			return nil
		}
		out = append(out, w.Text)
	}
	return out
}

func delegable(sub string, rest []shell.Word) bool {
	words := wordTexts(rest)
	if words == nil {
		return false
	}
	return Delegable(sub, words)
}

// Delegable reports whether an aw command is covered by a maintenance delegation. Starting agents,
// discarding work, granting anything, changing the pin and removing containers never are.
func Delegable(command string, args []string) bool {
	switch command {
	case "setup", "park", "unpark", "sweep":
		return true
	case "containers":
		return len(args) > 0 && args[0] == "up"
	}
	return false
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
	// What a lane shares with the owner checkout is aw's concern. The rest is the team's choice.
	safety := s.safeguard("git-safety")
	shared := s.Role == Lane || safety
	if unknown {
		if !shared {
			return allow()
		}
		return deny("git", "this git command has an option aw cannot determine, so it is refused → write its options literally")
	}
	for _, kv := range config {
		key, _, _ := strings.Cut(strings.ToLower(kv), "=")
		if shared && (key == "core.hookspath" || key == "core.fsmonitor" || key == "core.sshcommand" || strings.HasPrefix(key, "alias.")) {
			return deny("git", "git -c %s changes how Git runs hooks or commands, which bypasses the workspace's protections → run git without it", key)
		}
	}
	refused := func(what string) Decision {
		if !safety {
			return allow()
		}
		return deny("git", "git %s %s, which agents do not do → ask the human to do it, or leave it for review", sub, what)
	}
	// In a lane the work is the agent's own, so the human can approve it there. The owner checkout is
	// the human's, and force pushes and deleted branches cannot be taken back.
	destructive := func(what string) Decision {
		if !safety {
			return allow()
		}
		if s.Role == Lane {
			return ask("git", "git %s %s, in this lane → approve it if you asked for this", sub, what)
		}
		return refused(what)
	}
	switch sub {
	case "push":
		for _, a := range args {
			t := a.Text
			switch {
			case t == "-f" || t == "--force" || strings.HasPrefix(t, "--force-with-lease") || t == "--force-if-includes" || t == "--mirror":
				return refused("rewrites the remote's history")
			case t == "-d" || t == "--delete" || t == "--prune":
				return refused("deletes remote branches")
			case strings.HasPrefix(t, "+"):
				return refused("force-pushes a refspec")
			case strings.HasPrefix(t, ":") && len(t) > 1:
				return refused("deletes a remote branch")
			}
		}
		if hasShortFlag(args, 'f') || hasShortFlag(args, 'd') {
			return refused("forces or deletes")
		}
	case "branch":
		if d := s.laneBranchChange(args); d.Verdict != Allow {
			return d
		}
		if hasArg(args, "--delete") || hasShortFlag(args, 'd') || hasShortFlag(args, 'D') {
			return refused("deletes a branch")
		}
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
		// The ref hook refuses this too, but only after Git has moved the files, which leaves the
		// worktree full of staged changes. Refusing here leaves it untouched.
		if s.Role == Lane && hasArg(args, "-c", "-C", "--create", "--force-create", "--orphan", "-b", "-B") && !s.recreatesLaneBranch(args) {
			return deny("git", "git %s here would make a new branch, and a lane works only on its own branch → commit on this one, or run bin/aw attach from a new session for separate work; to publish under another name, run bin/aw push --as <name>", sub)
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
		if shared && gitConfigWrites(args) {
			return deny("git", "git config writes shared Git configuration, which the owner checkout and every lane use → ask the human")
		}
	case "remote":
		if st, _ := subcommand(args); shared && st != "" && st != "show" && st != "get-url" {
			return deny("git", "git remote %s changes shared Git configuration → ask the human", st)
		}
	case "gc", "prune", "repack":
		if hasArg(args, "--prune") || sub == "prune" {
			return destructive("deletes unreachable objects, which may hold other lanes' work")
		}
	}
	return allow()
}

// laneBranchChange refuses, in a lane, renaming or copying a branch and making a new one. The lane's
// branch is how aw knows the lane's work, and Git's ref hook never sees the new name of a rename, so
// a renamed lane branch would leave the lane unable to commit. This is aw's own, so it holds whatever
// safeguards are on. The lane's own branch may be made again, to recover.
func (s Scope) laneBranchChange(args []shell.Word) Decision {
	if s.Role != Lane {
		return allow()
	}
	own := "aw/<lane>"
	if len(s.LaneBranches) > 0 {
		own = s.LaneBranches[0]
	}
	publish := fmt.Sprintf("to publish it under another name, run bin/aw push --as <name>, which pushes %s to that name", own)
	if hasArg(args, "--move", "--copy") || hasShortFlag(args, 'm') || hasShortFlag(args, 'M') || hasShortFlag(args, 'c') || hasShortFlag(args, 'C') {
		return deny("git", "git branch would rename or copy a branch, and a lane keeps its branch, %s, which aw tracks its work by → %s", own, publish)
	}
	listing := hasArg(args, "--list", "--delete", "--set-upstream-to", "--unset-upstream", "--edit-description", "--contains", "--no-contains",
		"--merged", "--no-merged", "--points-at", "--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose") ||
		hasShortFlag(args, 'l') || hasShortFlag(args, 'd') || hasShortFlag(args, 'D') || hasShortFlag(args, 'u')
	names := nonOptions(args)
	if listing || len(names) == 0 || slices.Contains(s.LaneBranches, names[0]) {
		return allow()
	}
	return deny("git", "git branch %s would make a new branch, and a lane works only on its own branch, %s → commit on that one; %s", names[0], own, publish)
}

// recreatesLaneBranch reports whether git switch -c or checkout -b names the lane's own branch,
// which recovers a lane whose branch went missing.
func (s Scope) recreatesLaneBranch(args []shell.Word) bool {
	for i, a := range args {
		if (a.Text == "-c" || a.Text == "-C" || a.Text == "--create" || a.Text == "-b" || a.Text == "-B") && i+1 < len(args) {
			return args[i+1].Known && slices.Contains(s.LaneBranches, args[i+1].Text)
		}
	}
	return false
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
	if len(c.Args) == 0 || !s.safeguard("containers") || slices.Contains(s.RoleAllow, "containers") {
		return allow()
	}
	name := c.Name()
	args := c.Args[1:]
	refuse := func(what string) Decision {
		return deny("containers", "%s %s changes shared containers, which agents do not do → aw run runs a command in the lane's container; ask the human for anything else", name, what)
	}
	switch name {
	case "docker", "podman", "nerdctl":
		sub, rest := subcommandAfter(args, dockerValueOptions)
		switch sub {
		case "compose":
			if cs, _ := subcommandAfter(rest, composeValueOptions); containerChanging[cs] || cs == "create" {
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
		if cs, _ := subcommandAfter(args, composeValueOptions); containerChanging[cs] {
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
	if len(c.Args) == 0 || !s.safeguard("processes") {
		return allow()
	}
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
	if len(c.Args) == 0 || slices.Contains(s.RoleAllow, "servers") {
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

// secretLinkWrite refuses writing through a lane's link to a secret, which would change the owner's
// file from a lane. That is the lane boundary, so it holds whether or not secrets are guarded.
func (s Scope) secretLinkWrite(c shell.Command) Decision {
	if s.Role != Lane {
		return allow()
	}
	targets := writeTargets(c)
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) {
			targets = append(targets, r.Target)
		}
	}
	for _, t := range targets {
		if abs, ok := absolute(t.Text, c.Dir); t.Known && ok && s.isSecret(abs) {
			return deny("secrets", "%s is a link to the owner's file, which is not changed from a lane → ask the human to change it in the owner checkout", quote(t.Text))
		}
	}
	return allow()
}

var procEnviron = regexp.MustCompile(`^/proc/(self|thread-self|[0-9]+)(/task/[0-9]+)?/environ$`)

// secretRule refuses reading or echoing secrets (R68, F21): the secret files, the environment,
// and a process's environ.
func secretRule(s Scope, c shell.Command) Decision {
	if !s.safeguard("secrets") {
		return s.secretLinkWrite(c)
	}
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
			if hasArg(rest, "--list") || hasShortFlag(rest, 'l') {
				return !hasArg(rest, "--delete", "--move", "--copy", "--force") && !hasShortFlag(rest, 'd') && !hasShortFlag(rest, 'D') && !hasShortFlag(rest, 'm') && !hasShortFlag(rest, 'M') && !hasShortFlag(rest, 'c') && !hasShortFlag(rest, 'C')
			}
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
		return gitReadOnly[sub] && !hasArg(rest, "--output")
	case "curl":
		return curlOnlyReads(args)
	case "sed":
		return !hasArg(args, "--in-place") && !hasShortFlag(args, 'i')
	case "awk", "gawk":
		return !slices.ContainsFunc(args, func(a shell.Word) bool { return strings.Contains(a.Text, "inplace") })
	case "find":
		return !hasArg(args, "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls")
	}
	return readOnly[name]
}

// curlBareFlags and curlValueOptions are the curl options that neither write a file nor send a body.
// Anything else, such as -o, -d or -T, leaves the call unclassified.
var (
	curlBareFlags = map[string]bool{"--silent": true, "--show-error": true, "--head": true, "--location": true, "--fail": true,
		"--verbose": true, "--insecure": true, "--include": true, "--compressed": true, "--http1.1": true, "--http2": true}
	curlValueOptions = map[string]bool{"-m": true, "--max-time": true, "--connect-timeout": true, "-H": true, "--header": true, "-A": true, "--user-agent": true,
		"-w": true, "--write-out": true, "--retry": true}
)

// curlOnlyReads reports whether a curl call only asks for a URL and prints the answer. It is an
// allowlist, so that an option added to curl later is not read-only by default.
func curlOnlyReads(args []shell.Word) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.Known {
			return false
		}
		switch {
		case curlBareFlags[a.Text]:
		case curlValueOptions[a.Text]:
			i++
			// A header read from a file, or a format that writes one.
			if i >= len(args) || !args[i].Known || strings.HasPrefix(args[i].Text, "@") || strings.Contains(args[i].Text, "%output") {
				return false
			}
		case strings.HasPrefix(a.Text, "--"):
			return false
		case strings.HasPrefix(a.Text, "-"):
			if len(a.Text) == 1 || strings.Trim(a.Text[1:], "sSILfvki") != "" {
				return false
			}
		case strings.HasPrefix(a.Text, "file:"):
			return false
		}
	}
	return true
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
		// The destination, and any option's value that is a path (--target-directory=d,
		// --backup-dir=d, --log-file=f): an option aw does not know may write there.
		var out []shell.Word
		for _, a := range args {
			if opt, val, ok := strings.Cut(a.Text, "="); ok && strings.HasPrefix(opt, "--") && looksLikePath(val) {
				out = append(out, shell.Word{Text: val, Known: a.Known})
			}
		}
		for i, a := range args[:max(len(args)-1, 0)] {
			if a.Text == "-t" || a.Text == "--target-directory" {
				return append(out, args[i+1])
			}
		}
		if len(plain) > 0 {
			return append(out, plain[len(plain)-1])
		}
		return out
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
			return deny("self-modification", "this command writes to a path aw cannot read until it runs, so aw cannot tell whether that is the workspace's own configuration → write the path literally")
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
			if abs, ok := absolute(a.Text, c.Dir); a.Known && ok && (within(abs, filepath.Join(s.Owner, ".git", "aw")) || within(abs, filepath.Join(s.Owner, workspace.LocalStateDir))) {
				return deny("self-modification", "%s is aw's own record of this workspace, which agents do not change → ask the human", quote(a.Text))
			}
		}
	}
	return first
}

// unattachedRule claims lanes lazily, for what is in the owner's workspace. A session with no lane
// may read anywhere and write outside the workspace. A command that visibly changes the owner
// checkout is refused with the attach and pair instructions. A command aw cannot classify goes to
// the human, who can see what it is: a harness that cannot ask turns that into a refusal.
func unattachedRule(s Scope, c shell.Command) Decision {
	if s.Role != Unattached || isAW(c) || isReadOnly(c) {
		return allow()
	}
	if s.Delegated && len(c.Args) > 0 && c.Args[0].Known && slices.Contains(s.DelegatedCommands, c.Args[0].Text) {
		return allow()
	}
	what := "this command"
	if n := c.Name(); n != "" {
		what = n
	}
	targets, understood := unattachedWrites(c)
	if understood {
		unclear := false
		for _, t := range targets {
			abs, ok := "", false
			if t.Known {
				abs, ok = absolute(t.Text, c.Dir)
			}
			switch {
			case !ok:
				unclear = true
			case within(abs, s.Owner):
				return deny("attach", "%s changes %s, and this session is not attached to a lane: the owner checkout belongs to the human %s", what, quote(t.Text), s.attachHint())
			}
		}
		if !unclear {
			return allow()
		}
	}
	return ask("attach", "%s may change files, and this session is not attached to a lane, so the human decides → approve it if it only reads; otherwise %s", what, strings.TrimPrefix(s.attachHint(), "→ "))
}

// unattachedWrites are what a command writes, and whether that is all it can write: a reading command
// with redirects, or a plain file command. An interpreter's -i flag says nothing about what its
// program does.
func unattachedWrites(c shell.Command) ([]shell.Word, bool) {
	var out []shell.Word
	for _, r := range c.Redirs {
		if strings.Contains(r.Op, ">") && !isDup(r) && !harmless[r.Target.Text] {
			out = append(out, r.Target)
		}
	}
	if n := c.Name(); n == "perl" || n == "ruby" {
		return out, false
	}
	if files := writeTargets(c); len(files) > 0 {
		return append(out, files...), true
	}
	plain := c
	plain.Redirs = nil
	return out, isReadOnly(plain)
}

// publishAction classifies a command as one of the actions a team can pre-authorize, or "".
func publishAction(name, sub, sub2 string, c shell.Command, args []shell.Word) string {
	switch {
	case name == "git":
		if _, gs, _, _ := gitGlobal(args); gs == "push" {
			return "push"
		}
	case name == "gh" && sub == "pr" && sub2 == "create", name == "glab" && sub == "mr" && sub2 == "create":
		return "open-merge-request"
	case name == "gh" && sub == "pr" && sub2 == "comment", name == "glab" && sub == "mr" && sub2 == "note":
		return "open-merge-request"
	case name == "gh" && sub == "pr" && sub2 == "merge", name == "glab" && sub == "mr" && sub2 == "merge":
		return "merge"
	case isAW(c) && sub == "changeset":
		return "open-merge-request"
	case isAW(c) && sub == "push":
		return "push" // only ever the lane's own branch
	}
	return ""
}

// publishDecision applies the lane's role and the team's pre-authorization. nil means neither has a
// say, and the default applies.
func (s Scope) publishDecision(action string, c shell.Command, args []shell.Word) *Decision {
	capability := "publish"
	if action == "merge" {
		capability = "merge"
	}
	if s.Role == Lane && slices.Contains(s.RoleDeny, capability) {
		d := deny("role", "this lane's role does not %s → commit your work and report back; a session in another role, or the human, does that", capability)
		return &d
	}
	if s.Role != Lane {
		return nil
	}
	if !s.preauthorized(action, c) || (action == "push" && !isAW(c) && !s.pushesOwnBranch(args)) || (actsOnAnMR(args) && !s.ownMergeRequest(args)) {
		return nil
	}
	d := allow()
	return &d
}

// preauthorized reports whether this lane's role or the team's policy pre-authorizes an action here.
func (s Scope) preauthorized(action string, c shell.Command) bool {
	if s.Role != Lane {
		return false
	}
	capability := "publish"
	if action == "merge" {
		capability = "merge"
	}
	if slices.Contains(s.RoleAllow, capability) || slices.Contains(s.Publication, action) {
		return true
	}
	for dir, list := range s.ProjectPublication {
		if within(c.Dir.Physical, dir) && slices.Contains(list, action) {
			return true
		}
	}
	return false
}

// actsOnAnMR is a forge command on one existing merge request: merging or commenting on it. Creating
// one names no existing merge request.
func actsOnAnMR(args []shell.Word) bool {
	_, rest := subcommandAfter(args, forgeValueOptions)
	verb, _ := subcommandAfter(rest, forgeValueOptions)
	return verb == "merge" || verb == "comment" || verb == "note"
}

// forgeMRValueOptions take a value, so that the value is not mistaken for which merge request is meant.
var forgeMRValueOptions = map[string]bool{"-m": true, "--message": true, "-b": true, "--body": true, "-F": true, "--body-file": true,
	"-t": true, "--subject": true, "--match-head-commit": true, "--sha": true, "--squash-message": true}

// ownMergeRequest reports whether a forge command can only mean this lane's own merge request: with
// nothing named it uses the current branch, and a lane's own branch may be named. A number could be
// anyone's, and aw cannot look it up on this path. Another repository is never the lane's own.
func (s Scope) ownMergeRequest(args []shell.Word) bool {
	seen := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.Known {
			return false
		}
		if a.Text == "-R" || a.Text == "--repo" || strings.HasPrefix(a.Text, "--repo=") {
			return false
		}
		if forgeMRValueOptions[a.Text] {
			i++
			continue
		}
		if strings.HasPrefix(a.Text, "-") {
			continue
		}
		seen++
		if seen > 2 && !slices.Contains(s.LaneBranches, a.Text) { // past "pr merge" or "mr note"
			return false
		}
	}
	return true
}

// pushesOwnBranch reports whether a git push can only touch this lane's own branches. Anything it
// cannot be sure of is not pre-authorized, and so asks.
func (s Scope) pushesOwnBranch(args []shell.Word) bool {
	_, _, rest, unknown := gitGlobal(args)
	if unknown {
		return false
	}
	var positional []string
	for _, w := range rest {
		if !w.Known {
			return false
		}
		switch {
		case w.Text == "-u" || w.Text == "--set-upstream" || w.Text == "-q" || w.Text == "--quiet":
		case strings.HasPrefix(w.Text, "-"):
			return false
		default:
			positional = append(positional, w.Text)
		}
	}
	if len(positional) < 2 {
		return true // the remote's default: the current branch, which is the lane's
	}
	for _, ref := range positional[1:] {
		src, dst, mapped := strings.Cut(ref, ":")
		if !mapped {
			dst = src
		}
		dst = strings.TrimPrefix(dst, "refs/heads/")
		if strings.HasPrefix(src, "+") || (dst != "HEAD" && !slices.Contains(s.LaneBranches, dst) && !slices.Contains(s.PublishedBranches, dst)) {
			return false
		}
	}
	return true
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
	if name == "gh" || name == "glab" {
		sub, rest = subcommandAfter(args, forgeValueOptions)
		sub2, _ = subcommandAfter(rest, forgeValueOptions)
	}
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
	if isAW(c) && sub == "changeset" {
		what = "aw changeset pushes branches and opens merge requests"
	}
	if isAW(c) && sub == "push" {
		what = "aw push publishes the lane's commits to the remote"
	}
	if action := publishAction(name, sub, sub2, c, args); action != "" {
		if d := s.publishDecision(action, c, args); d != nil {
			return *d
		}
	}
	if what == "" || !s.safeguard("publication") {
		return allow()
	}
	// A refusal reaches the agent and a question reaches the human, so what the agent can fix by
	// writing the command another way is refused with the command, and never interrupts anyone.
	if action := publishAction(name, sub, sub2, c, args); action != "" && s.preauthorized(action, c) {
		switch {
		case actsOnAnMR(args) && !s.ownMergeRequest(args):
			return deny("publication", "%s, and only this lane's own merge request is pre-approved, which aw cannot tell from a number or another repository → run it from the project's worktree with no number, so that it means this lane's branch; if it is not this lane's, ask the human to do it", what)
		case action == "push" && !isAW(c) && !s.pushesOwnBranch(args):
			return deny("publication", "%s, and only this lane's own branch is pre-approved → run git push -u origin HEAD from the worktree; for anything else, ask the human to do it", what)
		}
	}
	return ask("publication", "%s, which needs the human's approval → approve it, or stop being asked by adding it to [publication] allow in workspace.toml or your own aw settings (push, open-merge-request, merge)", what)
}
