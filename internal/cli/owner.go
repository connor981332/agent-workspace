package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/forge"
	gitrun "github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/policy"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// delegatedHere reports whether a maintenance delegation covers this owner-only command, for a
// session in the owner checkout. Lanes never use one.
func delegatedHere(command string, args []string) bool {
	if !policy.Delegable(command, args) {
		return false
	}
	ws, _, err := loadConfig()
	if err != nil || ws.LaneRoot != "" {
		return false
	}
	dir, err := ws.StateDir()
	if err != nil {
		return false
	}
	s, err := state.Open(dir, state.ReadOnly)
	if err != nil {
		return false
	}
	defer s.Close()
	return state.DelegationActive(s.DB())
}

var ttlWord = regexp.MustCompile(`^([1-9][0-9]{0,3})([mh])$`)

// parseTTL accepts 30m or 2h, from 5 minutes to 8 hours.
func parseTTL(text string) (time.Duration, error) {
	m := ttlWord.FindStringSubmatch(text)
	if m == nil {
		return 0, &usageError{fmt.Errorf("%q is not a time to live → write it like 30m or 2h", text)}
	}
	n, _ := strconv.Atoi(m[1])
	d := time.Duration(n) * time.Minute
	if m[2] == "h" {
		d = time.Duration(n) * time.Hour
	}
	if d < 5*time.Minute || d > state.MaxDelegation {
		return 0, &usageError{fmt.Errorf("%s is out of range → a delegation lasts from 5m to 8h", text)}
	}
	return d, nil
}

func newDelegateCmd() *cobra.Command {
	var scope, ttl string
	var status, revoke, asJSON bool
	cmd := &cobra.Command{
		Use:   "delegate [--ttl 1h] | --status | --revoke",
		Short: "Let sessions in the owner checkout do routine maintenance, for a limited time",
		Long: `Grant sessions in the owner checkout routine maintenance for up to 8 hours: aw setup, park, unpark,
sweep --apply, containers up, and the commands in [delegation] maintenance. Lanes never use it.

It never covers starting agents, discarding work, pairing, delegating, upgrading, or recreating or
removing containers. Only the human can grant it. Maintenance is the only scope there is, and the
grant lasts an hour unless --ttl says otherwise.`,
		Args: cobra.NoArgs,
		RunE: run2(func(cmd *cobra.Command) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			switch {
			case revoke:
				if err := w.store.Write(ctx, state.Revoke); err != nil {
					return err
				}
			case status:
			default:
				if scope != state.ScopeMaintenance {
					return &usageError{fmt.Errorf("--scope must be %s", state.ScopeMaintenance)}
				}
				d, err := parseTTL(ttl)
				if err != nil {
					return err
				}
				if err := w.store.Write(ctx, func(tx *sql.Tx) error { _, err := state.Grant(tx, d); return err }); err != nil {
					return err
				}
			}
			current, active := state.CurrentDelegation(w.store.DB())
			if asJSON {
				return writeJSON(out, map[string]any{"delegated": active, "delegation": current})
			}
			if active {
				fmt.Fprintf(out, "Delegated %s to sessions in the owner checkout until %s.\n", current.Scope, current.Expires.Local().Format("15:04 Mon"))
			} else {
				fmt.Fprintln(out, "Nothing is delegated.")
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&scope, "scope", state.ScopeMaintenance, "what to delegate; maintenance is the only scope")
	cmd.Flags().StringVar(&ttl, "ttl", "1h", "how long, from 5m to 8h")
	cmd.Flags().BoolVar(&status, "status", false, "say what is delegated")
	cmd.Flags().BoolVar(&revoke, "revoke", false, "end the delegation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

// run2 adapts a command that takes no arguments.
func run2(f func(cmd *cobra.Command) error) func(*cobra.Command, []string) error {
	return run(func(cmd *cobra.Command, _ []string) error { return f(cmd) })
}

// ownerRepos are the owner checkout's repositories: the workspace, then the projects it has.
func ownerRepos(ws *workspace.Workspace, c *config.Config) []string {
	out := []string{ws.Owner}
	names := make([]string, 0, len(c.Projects))
	for name := range c.Projects {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if d := c.Projects[name].Directory; d != "." {
			out = append(out, filepath.Join(ws.Owner, d))
		}
	}
	return out
}

func gitDirty(ctx context.Context, dir string) (bool, error) {
	out, err := gitrun.In(dir).Run(ctx, "status", "--porcelain")
	return strings.TrimSpace(out) != "", err
}

func newMoveToLaneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   `move-to-lane "<purpose>"`,
		Short: "Move the owner checkout's uncommitted work into a new lane",
		Long: `Commit the owner checkout's uncommitted work to a new branch in each repository that has some,
put the owner checkout back on its branch, claim a lane on the new branch, and move a live pair
session there.`,
		Args: cobra.ExactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			if w.ws.LaneRoot != "" {
				return fmt.Errorf("aw move-to-lane moves the owner checkout's work, and this is a lane → run it in %s", ui.Clean(w.ws.Owner))
			}
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			name := lanes.Name(args[0])
			branch := w.cfg.Lanes.BranchPrefix + name
			var moved []string
			for _, dir := range ownerRepos(w.ws, w.cfg) {
				if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
					continue
				}
				dirty, err := gitDirty(ctx, dir)
				if err != nil {
					return err
				}
				if !dirty {
					continue
				}
				g := gitrun.In(dir)
				original, err := g.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
				if err != nil {
					return err
				}
				original = strings.TrimSpace(original)
				if original == "HEAD" {
					return fmt.Errorf("%s is on a detached HEAD → run aw try --back, or switch to a branch", ui.Clean(dir))
				}
				for _, step := range [][]string{
					{"switch", "-c", branch},
					{"add", "-A"},
					{"commit", "-m", args[0]},
					{"switch", original},
				} {
					if _, err := g.Run(ctx, step...); err != nil {
						return fmt.Errorf("moving the work in %s: git %s: %w → nothing else was moved; look at the branch %s there", ui.Clean(dir), step[0], err, branch)
					}
				}
				moved = append(moved, dir)
			}
			if len(moved) == 0 {
				return fmt.Errorf("the owner checkout has no uncommitted work to move → aw new starts a lane for new work")
			}
			m := manager(w)
			c, err := m.Claim(ctx, lanes.ClaimRequest{Purpose: args[0], Branch: branch})
			if err != nil {
				return fmt.Errorf("the work is committed on %s, but the lane could not be claimed: %w → run aw new with --branch %s", branch, err, branch)
			}
			var sessions int64
			if err := w.store.Write(ctx, func(tx *sql.Tx) error {
				var err error
				sessions, err = state.MovePairSession(tx, c.Lane.Number, c.Lane.Generation)
				return err
			}); err != nil {
				return err
			}
			startSetup(cmd.ErrOrStderr(), w, c)
			fmt.Fprintf(out, "Moved the work to lane %s %s, on %s.\n", c.Dir, ui.Clean(c.Lane.Name), branch)
			if sessions > 0 {
				fmt.Fprintln(out, "The pair session moved with it, and now works in the lane.")
			}
			return nil
		}),
	}
}

const tryKey = "try"

func newTryCmd() *cobra.Command {
	var back bool
	cmd := &cobra.Command{
		Use:   "try <lane> | --back",
		Short: "Check out a lane's exact heads in the owner checkout, to run them yourself",
		Long: `Check out a lane's exact heads, detached, in the owner checkout, so that you can run the work in
your own environment. It refuses when the owner checkout has uncommitted changes. aw try --back
returns each repository to the branch it was on.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			if w.ws.LaneRoot != "" {
				return fmt.Errorf("aw try changes the owner checkout, and this is a lane → run it in %s", ui.Clean(w.ws.Owner))
			}
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			saved, _ := w.store.Meta(tryKey)
			if back {
				if saved == "" {
					return fmt.Errorf("the owner checkout is not trying a lane")
				}
				places := map[string]string{}
				if err := json.Unmarshal([]byte(saved), &places); err != nil {
					return err
				}
				for dir, branch := range places {
					if _, err := gitrun.In(dir).Run(ctx, "switch", branch); err != nil {
						return fmt.Errorf("returning %s to %s: %w → commit or stash what changed there, then run this again", ui.Clean(dir), branch, err)
					}
				}
				if err := w.store.SetMeta(ctx, tryKey, ""); err != nil {
					return err
				}
				fmt.Fprintln(out, "Back on your branches.")
				return nil
			}
			if len(args) != 1 {
				return &usageError{fmt.Errorf("name a lane, or pass --back")}
			}
			if saved != "" {
				return fmt.Errorf("the owner checkout is already trying a lane → run aw try --back first")
			}
			if err := lanes.CheckName(args[0]); err != nil {
				return err
			}
			l, err := state.LaneByName(w.store.DB(), args[0])
			if err != nil {
				return fmt.Errorf("there is no lane named %s → aw shows the lanes", args[0])
			}
			repos, err := state.LaneRepos(w.store.DB(), l.Number)
			if err != nil {
				return err
			}
			heads, places := map[string]string{}, map[string]string{}
			for _, r := range repos {
				dir := w.ws.Owner
				if r.Project != "" {
					p := w.cfg.Projects[r.Project]
					if p == nil || p.Lanes == "shared" {
						continue
					}
					dir = filepath.Join(w.ws.Owner, p.Directory)
				}
				if dirty, err := gitDirty(ctx, dir); err != nil {
					return err
				} else if dirty {
					return fmt.Errorf("%s has uncommitted changes, so aw try would disturb them → commit or stash them first", ui.Clean(dir))
				}
				head, err := gitrun.In(r.Path).Run(ctx, "rev-parse", "HEAD")
				if err != nil {
					return err
				}
				branch, err := gitrun.In(dir).Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
				if err != nil {
					return err
				}
				heads[dir], places[dir] = strings.TrimSpace(head), strings.TrimSpace(branch)
			}
			data, err := json.Marshal(places)
			if err != nil {
				return err
			}
			if err := w.store.SetMeta(ctx, tryKey, string(data)); err != nil {
				return err
			}
			for dir, head := range heads {
				if _, err := gitrun.In(dir).Run(ctx, "switch", "--detach", head); err != nil {
					return fmt.Errorf("checking out %s in %s: %w → run aw try --back", head, ui.Clean(dir), err)
				}
			}
			fmt.Fprintf(out, "The owner checkout is on lane %s's exact heads. aw try --back returns it.\n", ui.Clean(l.Name))
			return nil
		}),
	}
	cmd.Flags().BoolVar(&back, "back", false, "return each repository to the branch it was on")
	return cmd
}

// changeOrder puts a change set's projects producer before consumer (R69). "" is the workspace
// repository, which goes last. A cycle is a configuration mistake.
func changeOrder(c *config.Config, changed []string) ([]string, error) {
	var out []string
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("projects depend on each other through %s, so no merge order exists → fix depends_on in workspace.toml", name)
		case 2:
			return nil
		}
		state[name] = 1
		if p := c.Projects[name]; p != nil {
			deps := slices.Sorted(slices.Values(p.DependsOn))
			for _, d := range deps {
				if slices.Contains(changed, d) {
					if err := visit(d); err != nil {
						return err
					}
				}
			}
		}
		state[name] = 2
		out = append(out, name)
		return nil
	}
	sorted := slices.Sorted(slices.Values(changed))
	for _, name := range sorted {
		if name != "" {
			if err := visit(name); err != nil {
				return nil, err
			}
		}
	}
	if slices.Contains(changed, "") {
		out = append(out, "")
	}
	return out, nil
}

type openedChange struct {
	Project string `json:"project"`
	URL     string `json:"url"`
	Order   int    `json:"order"`
}

func newChangesetCmd() *cobra.Command {
	var asJSON bool
	var title string
	cmd := &cobra.Command{
		Use:   "changeset --title <title>",
		Short: "Open this lane's linked merge requests, in merge order",
		Long: `Push this lane's branch in each repository that has commits, and open a merge request for each,
linked to the others and stating the merge order: producers before consumers, from depends_on in
workspace.toml. This is outward-facing, so the publication policy applies.`,
		Args: cobra.NoArgs,
		RunE: run2(func(cmd *cobra.Command) error {
			w, err := openForWrite()
			if err != nil {
				return err
			}
			defer w.Close()
			m := manager(w)
			n := m.CallerLane()
			if n == 0 {
				return fmt.Errorf("aw changeset opens a lane's merge requests → run it in a lane")
			}
			if title == "" {
				return &usageError{fmt.Errorf("--title is required")}
			}
			ctx := cmd.Context()
			repos, err := state.LaneRepos(w.store.DB(), n)
			if err != nil {
				return err
			}
			byProject := map[string]state.LaneRepo{}
			var changed []string
			for _, r := range repos {
				count, err := gitrun.In(r.Path).Run(ctx, "rev-list", "--count", r.BaseSHA+"..HEAD")
				if err != nil {
					return err
				}
				if strings.TrimSpace(count) != "0" {
					changed = append(changed, r.Project)
					byProject[r.Project] = r
				}
			}
			if len(changed) == 0 {
				return fmt.Errorf("this lane has no commits to open a merge request for → commit your work first")
			}
			order, err := changeOrder(w.cfg, changed)
			if err != nil {
				return err
			}
			label := func(p string) string {
				if p == "" {
					return "workspace"
				}
				return p
			}
			plan := make([]string, len(order))
			for i, p := range order {
				plan[i] = fmt.Sprintf("%d. %s", i+1, label(p))
			}
			body := func(links []string) string {
				text := "Part of a change set. Merge in this order:\n\n" + strings.Join(plan, "\n")
				if len(links) > 0 {
					text += "\n\nThe change set:\n\n- " + strings.Join(links, "\n- ")
				}
				return text
			}
			type target struct {
				client *forge.Client
				id     forge.Identity
				repo   state.LaneRepo
			}
			targets := make([]target, len(order))
			for i, p := range order {
				r := byProject[p]
				raw, err := gitrun.In(r.Path).Line(ctx, "config", "--get", "remote.origin.url")
				if err != nil {
					return fmt.Errorf("%s has no origin remote: %w", label(p), err)
				}
				host, project, ok := forge.ParseRemote(raw)
				client := forge.For(w.cfg.Workspace.Forge, host, time.Now().Add(time.Minute))
				if !ok || client == nil {
					return fmt.Errorf("%s's remote is not on a forge aw knows → set [workspace] forge, or open the merge request by hand", label(p))
				}
				targets[i] = target{client, forge.Identity{Host: host, Project: project, Target: strings.TrimPrefix(r.BaseRef, "origin/")}, r}
			}
			var opened []openedChange
			var links []string
			for i, t := range targets {
				if _, err := gitrun.In(t.repo.Path).Run(ctx, "push", "-u", "origin", t.repo.Branch); err != nil {
					return fmt.Errorf("pushing %s: %w → what is already open stays open; fix this and open the rest by hand", label(order[i]), err)
				}
				url, err := t.client.Open(ctx, t.id, t.repo.Branch, title, body(nil))
				if err != nil {
					return err
				}
				opened = append(opened, openedChange{Project: label(order[i]), URL: url, Order: i + 1})
				links = append(links, fmt.Sprintf("%s: %s", label(order[i]), url))
			}
			// Linked once every URL is known.
			for i, t := range targets {
				if err := t.client.Describe(ctx, t.id, opened[i].URL, t.repo.Branch, body(links)); err != nil {
					return err
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"changes": opened})
			}
			for _, o := range opened {
				fmt.Fprintf(cmd.OutOrStdout(), "%d. %s %s\n", o.Order, ui.Clean(o.Project), ui.Clean(o.URL))
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&title, "title", "", "the title of each merge request")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
