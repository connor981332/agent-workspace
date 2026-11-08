package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/launcher"
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
	Version  string  `json:"version"`
	Pinned   string  `json:"pinned"`
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
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the identity as JSON")
	return cmd
}
