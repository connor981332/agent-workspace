package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/launcher"
)

func newInitCmd() *cobra.Command {
	var opts app.InitOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Make the current Git repository a workspace, pinned to this aw",
		Long: `Make the current Git repository a workspace, pinned to this version of aw.

It writes workspace.toml (one project per Git repository found directly inside, or the repository
itself), .aw/version and .aw/lock (the pin and its checksums), bin/aw (the launcher), .aw/managed,
and the .gitignore entries. It refuses a directory that is already a workspace.

The checksums come from the release, or from $AW_MIRROR when it is set.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			opts.Dir = dir
			opts.Version = frameworkVersion()
			opts.Mirror = launcher.Mirror(opts.Version)
			res, err := app.Init(opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, res)
			}
			fmt.Fprintf(out, "Created the workspace %s (namespace %s), pinned to aw %s.\n", res.Owner, res.Namespace, res.Version)
			fmt.Fprintf(out, "Projects: %s\n", strings.Join(res.Projects, ", "))
			fmt.Fprintf(out, "Wrote: %s\n", strings.Join(res.Files, ", "))
			if len(res.Missing) > 0 {
				fmt.Fprintf(out, "Warning: the release has no binary for %s, so bin/aw will refuse to run there.\n", strings.Join(res.Missing, ", "))
			}
			fmt.Fprintln(out, "Next: review workspace.toml, commit these files, and run bin/aw doctor.")
			return nil
		}),
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "the workspace's display name (default: the directory's name)")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", "", "the workspace's namespace (default: from the directory's name)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print what was written as JSON")
	return cmd
}
