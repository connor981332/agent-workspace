package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/doctor"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newDoctorCmd() *cobra.Command {
	var asJSON, timings bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the workspace and this machine, and name the fix for every problem",
		Long: `Check the workspace and this machine: the configuration, the pin and its checksums, the
launcher, the state, the .gitignore entries, Git and the tools bin/aw needs.

It only reads, so it is safe before setup and from any lane. It exits 1 if any check fails.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			r := doctor.Run(cwd, frameworkVersion(), timings)
			out := cmd.OutOrStdout()
			if asJSON {
				if err := writeJSON(out, r); err != nil {
					return err
				}
			} else {
				color := ui.Color(os.Stdout) && out == os.Stdout
				marks := map[string]string{doctor.OK: "✓", doctor.Info: "·", doctor.Warn: "!", doctor.Fail: "✗"}
				colors := map[string]string{doctor.OK: "32", doctor.Info: "2", doctor.Warn: "33", doctor.Fail: "31"}
				for _, c := range r.Checks {
					mark := marks[c.Status]
					if color {
						mark = "\x1b[" + colors[c.Status] + "m" + mark + "\x1b[0m"
					}
					line := fmt.Sprintf("%s %s: %s", mark, c.Name, ui.Clean(c.Message))
					if c.Fix != "" {
						line += " → " + ui.Clean(c.Fix)
					}
					fmt.Fprintln(out, line)
				}
				for _, p := range r.Timings {
					fmt.Fprintf(out, "  timing  %-26s %8.3f ms\n", p.Name, float64(p.Duration)/float64(time.Millisecond))
				}
			}
			if !r.OK {
				return &silentError{fmt.Errorf("some checks failed")}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the checks as JSON")
	cmd.Flags().BoolVar(&timings, "timings", false, "also time each phase, to diagnose a slow workspace")
	return cmd
}
