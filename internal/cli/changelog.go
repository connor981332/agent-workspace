package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/audit"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// changelogJSON is aw changelog --json. Its field names are part of the interface.
type changelogJSON struct {
	Since   string         `json:"since"`
	Version string         `json:"version"`
	Entries []audit.Change `json:"entries"`
}

func newChangelogCmd() *cobra.Command {
	var asJSON bool
	var since string
	cmd := &cobra.Command{
		Use:   "changelog [--since <version>] [--json]",
		Short: "Show what changed in aw since a version: by default, since this workspace's configuration was last audited",
		Long: `Show what changed in aw since a version, newest first, from the changelog built into this aw.

--since defaults to [workspace] audited_with, the aw version that last designed or audited the
workspace's configuration (in a lane, the lane's own workspace.toml). Without it, or outside a
workspace, the whole changelog is shown.

With --json, each item also lists the configuration keys it names, so that an agent auditing the
workspace can tell which items touch keys the workspace sets.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			if since != "" && !config.VersionRE.MatchString(since) {
				return fmt.Errorf("%q is not a version → write it without a leading v, e.g. 0.2.1", since)
			}
			if !cmd.Flags().Changed("since") {
				since = auditedWith()
			}
			entries := audit.Changelog(since)
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, changelogJSON{Since: since, Version: frameworkVersion(), Entries: entries})
			}
			if len(entries) == 0 {
				if since == "" {
					_, err := fmt.Fprintln(out, "The changelog is empty.")
					return err
				}
				_, err := fmt.Fprintf(out, "Nothing has changed in aw since %s.\n", since)
				return err
			}
			if since != "" {
				fmt.Fprintf(out, "What changed in aw since %s, newest first:\n", since)
			}
			version := ""
			for _, e := range entries {
				if e.Version != version {
					version = e.Version
					fmt.Fprintf(out, "\n%s\n", version)
				}
				mark := "-"
				if e.Breaking {
					mark = "- Breaking:"
				}
				fmt.Fprintf(out, "%s %s\n", mark, ui.Clean(e.Text))
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&since, "since", "", "the version to start after (default: [workspace] audited_with)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the items, with the configuration keys each names, as JSON")
	return cmd
}

// auditedWith is [workspace] audited_with of the configuration around the working directory, or ""
// outside a workspace or when the configuration does not load.
func auditedWith() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return ""
	}
	c, err := config.Load(ws.ConfigDir())
	if err != nil {
		return ""
	}
	return c.Workspace.AuditedWith
}
