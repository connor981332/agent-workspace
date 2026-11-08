package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
	// Attached is the lane the environment's session is attached to, when that is certain.
	Attached *attachedInfo `json:"attached"`
	Warnings []string      `json:"warnings"`
	Version  string        `json:"version"`
	Pinned   string        `json:"pinned"`
}

type attachedInfo struct {
	Lane    string `json:"lane"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
}

// findAttachment reports the lane this session is attached to. It only reads, and it infers nothing
// from a record that is stale or ambiguous: it says so instead.
func findAttachment(stateDir string, max int) (*attachedInfo, []string) {
	warnings := []string{}
	ids, err := issuedIdentities()
	if err != nil || len(ids) == 0 {
		return nil, warnings
	}
	store, err := state.Open(stateDir, state.ReadOnly)
	if err != nil {
		return nil, warnings
	}
	defer store.Close()
	var found []*attachedInfo
	for _, id := range ids {
		rec, err := state.SessionByHarnessID(store.DB(), id.harness, id.id)
		if err != nil {
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
		return nil, append(warnings, "several harnesses' sessions are attached here, so aw cannot say which this is")
	}
	if len(found) == 1 && len(warnings) == 0 {
		return found[0], warnings
	}
	return nil, warnings
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
				info.Attached, info.Warnings = findAttachment(dir, c.Lanes.Max)
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
			for _, w := range info.Warnings {
				fmt.Fprintf(out, "warning    %s\n", w)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the identity as JSON")
	return cmd
}
