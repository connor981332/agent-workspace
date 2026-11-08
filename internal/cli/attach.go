package cli

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// harnessEnv names the variable through which each harness tells its tool commands their session.
var harnessEnv = []struct{ harness, env string }{
	{"claude", "CLAUDE_CODE_SESSION_ID"}, {"codex", "CODEX_THREAD_ID"}, {"grok", "GROK_SESSION_ID"},
}

// sessionIdentity reads who is asking, from what the harness (or aw new, through AW_SESSION) put in
// the environment. --harness chooses among several; --harness and --session are checked against
// the environment, because an identity the harness did not issue could claim another session's lane.
func sessionIdentity(harness, session string) (lanes.Identity, error) {
	if session != "" && harness == "" {
		return lanes.Identity{}, fmt.Errorf("--harness and --session go together → pass both, or neither")
	}
	type issued struct{ harness, id, source string }
	var found []issued
	if env := os.Getenv("AW_SESSION"); env != "" {
		h, id, ok := strings.Cut(env, ":")
		if !ok || h == "" || id == "" {
			return lanes.Identity{}, fmt.Errorf("AW_SESSION is %q, not <harness>:<session id> → unset it, or start the session with aw new", env)
		}
		found = append(found, issued{h, id, "AW_SESSION"})
	} else {
		for _, e := range harnessEnv {
			if id := os.Getenv(e.env); id != "" {
				found = append(found, issued{e.harness, id, e.env})
			}
		}
	}
	if harness != "" {
		found = slices.DeleteFunc(found, func(i issued) bool { return i.harness != harness })
		if len(found) == 0 {
			return lanes.Identity{}, fmt.Errorf("this is not running inside a %s session that aw can verify, so it is refused → run aw attach from the harness, without --harness or --session", harness)
		}
	}
	switch len(found) {
	case 0:
		return lanes.Identity{}, nil // a person at a terminal: no session to record
	case 1:
	default:
		return lanes.Identity{}, fmt.Errorf("several harnesses' session IDs are set (%s, %s) → choose with --harness", found[0].harness, found[1].harness)
	}
	if session != "" && session != found[0].id {
		return lanes.Identity{}, fmt.Errorf("--session %q is not the session %s says this is, so it is refused → run aw attach without --session", session, found[0].source)
	}
	who := lanes.Identity{Harness: found[0].harness, Session: found[0].id}
	if pid, err := strconv.Atoi(os.Getenv("AW_SESSION_PID")); err == nil && pid > 0 {
		who.PID = pid
	} else {
		// A harness's sandbox may hide it; then the session's first hook records it.
		who.PID = platform.HarnessAncestor(os.Getpid(), who.Harness)
	}
	return who, nil
}

func newAttachCmd() *cobra.Command {
	var asJSON bool
	var branch, harness, session string
	cmd := &cobra.Command{
		Use:   `attach "<purpose>"`,
		Short: "Claim a lane for this session and say where to work",
		Long: `Claim a lane for the work described by the purpose: a warm lane from the pool if there is one, or
a new one. The lane is named from the purpose (aw attach "fix checkout tax rounding" gives
checkout-tax), and has a worktree of every project on the lane's branch.

The same session attaching again gets the same lane. Work in the lane root aw prints.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			who, err := sessionIdentity(harness, session)
			if err != nil {
				return err
			}
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := &lanes.Manager{WS: w.ws, Cfg: w.cfg, Store: w.store}
			c, err := m.Claim(cmd.Context(), lanes.ClaimRequest{Purpose: args[0], Branch: branch, Who: who})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{
					"number": c.Lane.Number, "lane": c.Dir, "name": c.Lane.Name, "purpose": c.Lane.Purpose,
					"state": c.Lane.State, "root": c.Root, "repos": c.Repos, "reused": c.Reused, "from_pool": c.FromPool,
				})
			}
			verb := "Claimed"
			if c.Reused {
				verb = "Already attached to"
			}
			fmt.Fprintf(out, "%s lane %s %s for %q.\n", verb, c.Dir, ui.Clean(c.Lane.Name), ui.Clean(c.Lane.Purpose))
			fmt.Fprintf(out, "Work in the lane root: cd %s\n", shellQuote(c.Root))
			return nil
		}),
	}
	cmd.Flags().StringVar(&branch, "branch", "", "start on this existing branch (tracking origin's when it is only there) instead of a new one")
	cmd.Flags().StringVar(&harness, "harness", "", "the harness of the attaching session (normally from AW_SESSION or the hook)")
	cmd.Flags().StringVar(&session, "session", "", "the harness's session ID (normally from AW_SESSION or the hook)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the lane as JSON")
	return cmd
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
