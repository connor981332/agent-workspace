package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/broker"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/launcher"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/timing"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// whoamiInfo is aw whoami --json. It never contains a secret value: no environment values and no
// configuration values beyond the workspace's name.
type whoamiInfo struct {
	Workspace struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"workspace"`
	Owner    string  `json:"owner"`
	Location string  `json:"location"` // owner or lane
	LaneRoot *string `json:"lane_root"`
	Session  *string `json:"session"`
	// Pair is whether this session is a pair session in the owner checkout.
	Pair bool `json:"pair"`
	// Attached is the lane the environment's session is attached to, when that is certain.
	Attached *attachedInfo `json:"attached"`
	// Seats are what this lane holds, with its URLs and tokens.
	Seats    []served `json:"seats"`
	Warnings []string `json:"warnings"`
	Version  string   `json:"version"`
	Pinned   string   `json:"pinned"`
}

type attachedInfo struct {
	Lane    string `json:"lane"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
}

// findAttachment reports the lane this session is attached to. It only reads, and it infers nothing
// from a record that is stale or ambiguous: it says so instead.
func findAttachment(stateDir string, max int) (*attachedInfo, bool, []string) {
	warnings := []string{}
	pair := false
	ids, err := issuedIdentities()
	if err != nil || len(ids) == 0 {
		return nil, false, warnings
	}
	store, err := state.Open(stateDir, state.ReadOnly)
	if err != nil {
		return nil, false, warnings
	}
	defer store.Close()
	var found []*attachedInfo
	for _, id := range ids {
		rec, err := state.SessionByHarnessID(store.DB(), id.harness, id.id)
		if err != nil {
			continue
		}
		if rec.Pair {
			pair = pair || (rec.State == state.SessionActive && rec.Alive())
			continue
		}
		if rec.Lane == 0 {
			warnings = append(warnings, "this session's attachment is stale: its lane has been recycled or destroyed → run aw attach before editing")
			continue
		}
		l, err := state.LaneByNumber(store.DB(), rec.Lane)
		if err != nil || l.Generation != rec.LaneGeneration {
			warnings = append(warnings, "this session's attachment is stale: its lane has moved on → run aw attach before editing")
			continue
		}
		if rec.State != state.SessionActive || !rec.Alive() {
			warnings = append(warnings, "this session's attachment has ended → run aw attach before editing")
			continue
		}
		found = append(found, &attachedInfo{Lane: lanes.Dir(l.Number, max), Name: ui.Clean(l.Name), Harness: id.harness})
	}
	if len(found) > 1 {
		return nil, pair, append(warnings, "several harnesses' sessions are attached here, so aw cannot say which this is")
	}
	if len(found) == 1 && len(warnings) == 0 {
		return found[0], pair, warnings
	}
	return nil, pair, warnings
}

// seatsOf reports a lane's seats. It only reads: a holder that looks dead is mentioned, and left for
// the next command that changes something to recover.
func seatsOf(stateDir string, c *config.Config, lane int) ([]served, []string) {
	out, warnings := []served{}, []string{}
	store, err := state.Open(stateDir, state.ReadOnly)
	if err != nil {
		return out, warnings
	}
	defer store.Close()
	leases, err := state.Leases(store.DB())
	if err != nil {
		return out, warnings
	}
	b := &broker.Broker{Cfg: c}
	for _, l := range leases {
		if l.Lane != lane {
			continue
		}
		r, err := b.Lookup(l.Resource)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("this lane holds a seat on %s, which the configuration no longer has → run aw stop %s", l.Resource, l.Resource))
			continue
		}
		ps, _ := state.LeaseProcs(store.DB(), l.ID)
		if b.Dead(l, ps) {
			warnings = append(warnings, fmt.Sprintf("the processes behind this lane's seat on %s have gone, so its URLs may not answer → run aw serve %s", l.Resource, l.Resource))
		}
		if l.State == state.LeaseReserved {
			warnings = append(warnings, fmt.Sprintf("a seat on %s is reserved for this lane, and not taken up yet → run aw serve %s", l.Resource, l.Resource))
			continue
		}
		s := served{Resource: l.Resource, Seat: l.Seat, Token: l.Token}
		if r.Kind == "process" {
			s.URLs = urlsOf(c, r, l)
		}
		out = append(out, s)
	}
	return out, warnings
}

func newWhoamiCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show where this command runs: the workspace, the owner checkout or a lane, and the session",
		Long: `Show where this command runs: the workspace, whether this is the owner checkout or a lane, the
session, and the aw version. It only reads, and it works before setup.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t := timing.Start()
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			t.Mark("config")
			var info whoamiInfo
			info.Workspace.Name = ui.Clean(c.Workspace.Name)
			info.Workspace.Namespace = c.Workspace.Namespace
			info.Owner = ws.Owner
			info.Location = "owner"
			if ws.LaneRoot != "" {
				info.Location = "lane"
				info.LaneRoot = &ws.LaneRoot
			}
			if s := os.Getenv("AW_SESSION"); s != "" {
				clean := ui.Clean(s)
				info.Session = &clean
			}
			if dir, err := ws.StateDir(); err == nil {
				info.Attached, info.Pair, info.Warnings = findAttachment(dir, c.Lanes.Max)
			}
			info.Seats = []served{}
			if ws.LaneRoot != "" {
				if dir, err := ws.StateDir(); err == nil {
					if n, err := strconv.Atoi(filepath.Base(ws.LaneRoot)); err == nil {
						var more []string
						info.Seats, more = seatsOf(dir, c, n)
						info.Warnings = append(info.Warnings, more...)
					}
				}
			}
			info.Version = frameworkVersion()
			if data, err := os.ReadFile(filepath.Join(ws.Owner, launcher.VersionPath)); err == nil {
				info.Pinned = strings.TrimSpace(string(data))
			}
			t.Mark("identity")
			if timing.Debug() {
				defer t.Report(cmd.ErrOrStderr(), "whoami")
			}

			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, info)
			}
			fmt.Fprintf(out, "workspace  %s (namespace %s)\n", info.Workspace.Name, info.Workspace.Namespace)
			fmt.Fprintf(out, "owner      %s\n", ui.Clean(info.Owner))
			if info.LaneRoot != nil {
				fmt.Fprintf(out, "location   lane root %s\n", ui.Clean(*info.LaneRoot))
			} else {
				fmt.Fprintln(out, "location   the owner checkout")
				if info.Attached != nil {
					fmt.Fprintf(out, "attached   lane %s %s\n", info.Attached.Lane, info.Attached.Name)
				}
				if info.Pair {
					fmt.Fprintln(out, "role       pair session, granted by the human")
				}
			}
			if info.Session != nil {
				fmt.Fprintf(out, "session    %s\n", *info.Session)
			} else {
				fmt.Fprintln(out, "session    none")
			}
			pin := ""
			if info.Pinned != "" && info.Pinned != info.Version {
				pin = fmt.Sprintf(" (the workspace pins %s → run bin/aw)", ui.Clean(info.Pinned))
			}
			fmt.Fprintf(out, "aw         %s%s\n", info.Version, pin)
			for _, s := range info.Seats {
				fmt.Fprintf(out, "seat       %d on %s", s.Seat, s.Resource)
				if s.Token != "" {
					fmt.Fprintf(out, ", token %s", ui.Clean(s.Token))
				}
				for _, name := range slices.Sorted(maps.Keys(s.URLs)) {
					fmt.Fprintf(out, ", %s %s", name, s.URLs[name])
				}
				fmt.Fprintln(out)
			}
			for _, w := range info.Warnings {
				fmt.Fprintf(out, "warning    %s\n", w)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the identity as JSON")
	return cmd
}
