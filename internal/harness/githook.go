package harness

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// GitHook is the Git hooks dispatcher (owner-checkout.md, "The Git hooks dispatcher"): aw's own
// check for reference-transaction, then the team's hook of the same name. It runs in a lane only:
// in the owner checkout the dispatcher script never starts aw, so plain Git there needs no aw.
//
// Any error refuses: Git aborts a transaction whose prepared hook exits non-zero.
func GitHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aw git-hook: usage: aw git-hook <hook> [arguments] → the hooks dispatcher (aw setup writes it) runs this")
		return Deny
	}
	name, rest := args[0], args[1:]
	if name != "reference-transaction" {
		return execTeamHook(name, rest, stderr)
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "aw git-hook: reference-transaction takes its state as its one argument, so this ref update is refused")
		return Deny
	}
	input, err := io.ReadAll(io.LimitReader(stdin, maxPayload+1))
	if err != nil || len(input) > maxPayload {
		fmt.Fprintf(stderr, "aw: the ref update cannot be read (%v), so it is refused\n", err)
		return Deny
	}
	// Decide at prepared only: 2.56 adds preparing, and committed or aborted cannot be refused.
	if rest[0] == "prepared" {
		if err := checkRefs(input); err != nil {
			fmt.Fprintf(stderr, "aw: %v\n", err)
			return Deny
		}
	}
	return runTeamHook(name, rest, input, stdout, stderr)
}

// worktreePrivate are refs that belong to one worktree, under refs/.
var worktreePrivate = []string{"refs/bisect/", "refs/worktree/", "refs/rewritten/"}

func checkRefs(input []byte) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if os.Getenv("GIT_DIR") == "." {
		// git receive-pack: a push into this repository, such as git push . HEAD:main from a lane. It
		// runs in the Git directory, so nothing here says which checkout it came from.
		return checkLines(input, nil, cwd, func(refused []string) error {
			return fmt.Errorf("a push into this repository cannot be traced to a checkout, so it is refused: %s → update the branch from the owner checkout, with git merge or git branch", strings.Join(refused, ", "))
		})
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return fmt.Errorf("this ref update is refused: %v", err)
	}
	if ws.LaneRoot == "" {
		return nil // the owner checkout, reached with GIT_DIR set
	}
	stateDir, err := ws.StateDir()
	if err != nil {
		return err
	}
	s, err := state.Open(stateDir, state.ReadOnly)
	if err != nil {
		return fmt.Errorf("the state cannot be read (%v), so this ref update is refused → run aw doctor", err)
	}
	defer s.Close()
	if tok := os.Getenv("AW_REF_TOKEN"); tok != "" && validRefToken(s, tok) {
		return nil // aw's own operation
	}
	n, err := strconv.Atoi(filepath.Base(ws.LaneRoot))
	if err != nil {
		return fmt.Errorf("%s is not a lane directory, so this ref update is refused", ws.LaneRoot)
	}
	repos, err := state.LaneRepos(s.DB(), n)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("lane %s has no record in the state, so this ref update is refused → run aw doctor", filepath.Base(ws.LaneRoot))
	}
	var own []string
	for _, r := range repos {
		if r.Branch != "" {
			own = append(own, "refs/heads/"+r.Branch)
		}
	}
	common, err := workspace.GitCommonDir(cwd)
	if err != nil {
		return err
	}
	return checkLines(input, own, common, func(refused []string) error { return refusal(refused, own) })
}

// checkLines applies the rule to each line of a transaction: <old> <new> <ref>.
func checkLines(input []byte, own []string, common string, fail func([]string) error) error {
	var refused []string
	for _, line := range strings.Split(strings.TrimSpace(string(input)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return fmt.Errorf("the ref update %q cannot be read, so it is refused", line)
		}
		if !refAllowed(f[0], f[1], f[2], own, common) {
			refused = append(refused, f[2])
		}
	}
	if len(refused) > 0 {
		return fail(refused)
	}
	return nil
}

func refusal(refs, own []string) error {
	hint := "work on the lane's own branch"
	if len(own) > 0 {
		hint = "work on the lane's branch, " + strings.TrimPrefix(own[0], "refs/heads/")
	}
	if slices.Contains(refs, "refs/stash") {
		hint = "the stash is shared by every worktree, so commit to the lane's branch instead"
	}
	return fmt.Errorf("a lane may change only its own branches, so this ref update is refused: %s → %s", strings.Join(refs, ", "), hint)
}

// refAllowed is the rule for a ref update made from a lane (S3 and docs/dev/spikes.md).
func refAllowed(old, new, ref string, own []string, common string) bool {
	// Unchanged. Zero to zero is not: git branch -D reports a deletion that way, because an old value
	// of zero only means the caller did not check it.
	if old == new && !isZero(new) {
		return true
	}
	if !strings.Contains(ref, "/") || strings.HasPrefix(ref, "refs/remotes/") || slices.Contains(own, ref) {
		return true // HEAD and the other pseudo-refs are private to the worktree
	}
	for _, p := range worktreePrivate {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return unchangedByPacking(old, new, ref, common)
}

// unchangedByPacking recognizes git pack-refs (and so git gc), which touches every ref in two
// transactions: writing its current value to packed-refs (zero → value), then removing the loose
// copy (value → zero). Neither changes what the ref resolves to. A real deletion of a packed ref
// has no loose copy, and -f to a new value does not match the current one.
func unchangedByPacking(old, new, ref, common string) bool {
	loose := looseRef(common, ref)
	switch {
	case isZero(old) && !isZero(new):
		return new == loose || (loose == "" && new == packedRef(common, ref))
	case !isZero(old) && isZero(new):
		return loose == old && packedRef(common, ref) == old
	}
	return false
}

var zeroOID = regexp.MustCompile(`^0+$`)

func isZero(oid string) bool { return zeroOID.MatchString(oid) }

func looseRef(common, ref string) string {
	data, err := os.ReadFile(filepath.Join(common, filepath.FromSlash(ref)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func packedRef(common, ref string) string {
	data, err := os.ReadFile(filepath.Join(common, "packed-refs"))
	if err != nil {
		return ""
	}
	suffix := []byte(" " + ref)
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasSuffix(line, suffix) && !bytes.HasPrefix(line, []byte("#")) && !bytes.HasPrefix(line, []byte("^")) {
			return string(line[:len(line)-len(suffix)])
		}
	}
	return ""
}

// validRefToken checks a token aw recorded in state for its own ref updates (S3).
func validRefToken(s *state.Store, token string) bool {
	expires, err := s.Meta(state.RefTokenKey(token))
	if err != nil || expires == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, expires)
	return err == nil && time.Now().Before(t)
}

// teamHook is the team's hook of this name, if it has one. AW_TEAM_HOOKS is set by the dispatcher
// script; a relative path is relative to the worktree, where Git runs hooks.
func teamHook(name string) string {
	dir := os.Getenv("AW_TEAM_HOOKS")
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, name)
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	return path
}

// execTeamHook replaces this process with the team's hook, which inherits stdin and stdout.
func execTeamHook(name string, args []string, stderr io.Writer) int {
	path := teamHook(name)
	if path == "" {
		return Allow
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(stderr, "aw: the team's %s hook cannot be found (%v), so it is refused\n", name, err)
		return Deny
	}
	err = syscall.Exec(abs, append([]string{abs}, args...), os.Environ())
	fmt.Fprintf(stderr, "aw: the team's %s hook cannot start (%v), so it is refused\n", name, err)
	return Deny
}

// runTeamHook runs the team's hook with the input aw already read from stdin.
func runTeamHook(name string, args []string, input []byte, stdout, stderr io.Writer) int {
	path := teamHook(name)
	if path == "" {
		return Allow
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(stderr, "aw: the team's %s hook cannot be found (%v), so it is refused\n", name, err)
		return Deny
	}
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(stderr, "aw: %v\n", err)
		return Deny
	}
	proc, err := os.StartProcess(abs, append([]string{abs}, args...), &os.ProcAttr{Files: []*os.File{r, os.Stdout, os.Stderr}})
	r.Close()
	if err != nil {
		w.Close()
		fmt.Fprintf(stderr, "aw: the team's %s hook cannot start (%v), so it is refused\n", name, err)
		return Deny
	}
	go func() {
		w.Write(input) //nolint:errcheck // a hook that does not read its input is not an error
		w.Close()
	}()
	st, err := proc.Wait()
	if err != nil {
		fmt.Fprintf(stderr, "aw: the team's %s hook: %v\n", name, err)
		return Deny
	}
	if !st.Success() {
		return Deny
	}
	return Allow
}
