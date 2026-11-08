package cli

import (
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
)

// version is the framework version. Release builds set it with
// -ldflags "-X github.com/connor981332/agent-workspace/internal/cli.version=v1.2.3".
var version string

// versionInfo is the --json output of aw version. Its field names are part of the interface.
type versionInfo struct {
	Version      string `json:"version"`
	ConfigSchema int    `json:"config_schema"`
	StateSchema  int    `json:"state_schema"`
}

func currentVersion() versionInfo {
	return versionInfo{Version: frameworkVersion(), ConfigSchema: config.SchemaVersion, StateSchema: state.SchemaVersion}
}

// frameworkVersion is the version set at link time, else the module version recorded by
// go install, else "dev".
func frameworkVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the framework, configuration schema and state schema versions",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			v := currentVersion()
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(v)
			}
			_, err := fmt.Fprintf(out, "aw %s (config schema %d, state schema %d)\n",
				v.Version, v.ConfigSchema, v.StateSchema)
			return err
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the versions as JSON")
	return cmd
}
