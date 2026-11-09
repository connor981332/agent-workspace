package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/doctor"
	"github.com/connor981332/agent-workspace/internal/ui"
)

func newSetupCmd() *cobra.Command {
	var asJSON, startContainers bool
	var harnessName string
	cmd := &cobra.Command{
		Use:   "setup [lane]",
		Short: "Set up this workspace after cloning it, or rerun a lane's setup from its first incomplete step",
		Long: `Set up this workspace on this machine: clone the project repositories, create your individual
configuration, check the secret files, install aw's Git hooks dispatcher and each harness's hooks and
sandbox settings, install dependencies in the owner checkout, and run aw doctor. Safe to rerun.

Containers start only if you say so: on a terminal aw asks, and otherwise pass --containers.

With a lane, rerun that lane's setup in the foreground, from its first incomplete step. An agent may
do this for its own lane.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return retrySetup(cmd, args[0])
			}
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			progress := cmd.OutOrStdout()
			if asJSON {
				progress = errOut // standard output is the JSON
			}
			if err := cloneProjects(cmd.Context(), ws.Owner, c, progress, errOut); err != nil {
				return err
			}
			if err := ensureIndividual(c, harnessName, cmd.InOrStdin(), progress); err != nil {
				return err
			}
			reportSecrets(ws.Owner, c, errOut)
			results, err := app.InstallGitHooks(context.Background(), ws.Owner, c)
			if err != nil {
				return err
			}
			configs, err := app.InstallHarnessConfig(ws.Owner)
			if err != nil {
				return err
			}
			if err := app.RegisterWorkspace(ws.Owner); err != nil {
				return err
			}
			instructions, err := compose.Apply(cmd.Context(), ws.Owner, c.Workspace.Name, c.Workspace.Namespace)
			if err != nil {
				return err
			}
			skills, skillEdits, err := compose.ApplySkills(ws.Owner)
			if err != nil {
				return err
			}
			for _, f := range skillEdits {
				fmt.Fprintf(errOut, "warning: %s was edited by hand, so aw left it alone\n", f)
			}
			sandboxes, err := setupSandboxes(ws, c)
			if err != nil {
				return err
			}
			installFailed := installOwner(cmd, ws, c, startContainers, asJSON)
			report := doctor.Run(ws.Owner, frameworkVersion(), false)
			var finish error
			switch {
			case !report.OK:
				finish = &silentError{errors.New("aw doctor found problems")}
			case installFailed != nil:
				finish = &silentError{installFailed}
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), map[string]any{"repos": results, "harness_config": configs, "instructions": instructions, "sandboxes": sandboxes, "doctor": report}); err != nil {
					return err
				}
				return finish
			}
			out := cmd.OutOrStdout()
			for _, r := range results {
				switch {
				case r.Missing:
					fmt.Fprintf(os.Stderr, "%s is not cloned yet, so it has no hooks\n", ui.Clean(r.Repo))
				case r.Installed:
					fmt.Fprintf(out, "%s: installed the hooks dispatcher\n", ui.Clean(r.Repo))
				default:
					fmt.Fprintf(out, "%s: already set up\n", ui.Clean(r.Repo))
				}
			}
			changed := false
			for _, c := range configs {
				if c.Changed {
					changed = true
					fmt.Fprintf(out, "%s: wrote aw's hooks\n", ui.Clean(c.Path))
				}
			}
			for _, r := range instructions {
				switch {
				case r.Edited:
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", r.Note)
				case r.Changed && r.Path != compose.PersonalPath:
					changed = true
					fmt.Fprintf(out, "%s: wrote the instructions\n", r.Path)
				case r.Changed:
					fmt.Fprintf(out, "%s: wrote your personal instructions (Git ignores this file)\n", r.Path)
				}
			}
			for _, r := range sandboxes {
				if r.Note != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", r.Path, r.Note)
				}
			}
			changed = changed || len(skills) > 0
			if changed {
				fmt.Fprintln(out, "Next: commit these files, so lanes get them. Codex asks once to trust project hooks, and Grok to trust the folder (/hooks-trust).")
			}
			problems := 0
			for _, c := range report.Checks {
				if c.Status == doctor.Warn || c.Status == doctor.Fail {
					problems++
					line := fmt.Sprintf("%s: %s", c.Name, ui.Clean(c.Message))
					if c.Fix != "" {
						line += " → " + ui.Clean(c.Fix)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "doctor: %s\n", line)
				}
			}
			if problems == 0 {
				fmt.Fprintln(out, "aw doctor found nothing wrong.")
			}
			if finish == nil {
				fmt.Fprintln(out, "Set up. Next: aw new \"<purpose>\", or start a harness here.")
			}
			return finish
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	cmd.Flags().BoolVar(&startContainers, "containers", false, "start the devcontainers without asking")
	cmd.Flags().StringVar(&harnessName, "harness", "", "the default harness for your individual configuration, without asking")
	return cmd
}
