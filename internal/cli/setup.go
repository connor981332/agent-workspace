package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/doctor"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

func newSetupCmd() *cobra.Command {
	var asJSON, startContainers bool
	var harnessName string
	var projects []string
	cmd := &cobra.Command{
		Use:   "setup [lane [--project p]…]",
		Short: "Set up this workspace after cloning it, or rerun a lane's setup from its first incomplete step",
		Long: `aw onboard runs this first, so you only need it alone on a fresh clone of a workspace that is already
configured, or to retry a lane.

Set up this workspace on this machine: clone the project repositories, create your individual
configuration, check the secret files, install aw's Git hooks dispatcher and each harness's hooks and
sandbox settings, install dependencies in the owner checkout, and run aw doctor. Safe to rerun.

Containers start only if you say so: on a terminal aw asks, and otherwise pass --containers.

With a lane, rerun that lane's setup in the foreground, from its first incomplete step. An agent may
do this for its own lane. With lazy setup ([lanes] setup = "lazy"), a project's own steps otherwise
wait for its first aw run, aw serve or team command; --project sets up only the projects named, and
without it every project is set up.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return retrySetup(cmd, args[0], projects)
			}
			if len(projects) > 0 {
				return &usageError{errors.New("--project sets up a project in a lane → name the lane: aw setup <lane> --project <name>")}
			}
			ws, c, err := loadConfig()
			if err != nil {
				return err
			}
			return setupWorkspace(cmd, ws, c, harnessName, startContainers, asJSON)
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	cmd.Flags().BoolVar(&startContainers, "containers", false, "start the devcontainers without asking")
	cmd.Flags().StringVar(&harnessName, "harness", "", "the default harness for your individual configuration, without asking")
	cmd.Flags().StringSliceVar(&projects, "project", nil, "with a lane: set up only this project (repeatable)")
	return cmd
}

// setupWorkspace is aw setup for the workspace, which aw onboard runs first. It is safe to rerun.
func setupWorkspace(cmd *cobra.Command, ws *workspace.Workspace, c *config.Config, harnessName string, startContainers, asJSON bool) error {
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
	fmt.Fprintln(progress, "Installing aw's hooks and instructions")
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
	var already []string
	for _, r := range results {
		name := ui.Clean(repoName(c, r.Repo))
		switch {
		case r.Missing:
			fmt.Fprintf(errOut, "%s is not cloned yet, so it has no hooks\n", name)
		case r.Installed:
			fmt.Fprintf(out, "%s: installed the hooks dispatcher\n", name)
		default:
			already = append(already, name)
		}
	}
	if len(already) > 0 {
		fmt.Fprintf(out, "Hooks dispatcher already set up in %s\n", strings.Join(already, ", "))
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
		fmt.Fprintln(out, "Lanes work with these files as they are; commit them when you are ready. Codex asks once to trust project hooks, and Grok to trust the folder (/hooks-trust).")
	}
	problems := 0
	warn := ui.NewOut(cmd.ErrOrStderr())
	for _, c := range report.Checks {
		if c.Status == doctor.Warn || c.Status == doctor.Fail {
			problems++
			line := fmt.Sprintf("%s: %s", c.Name, ui.Clean(c.Message))
			if c.Fix != "" {
				line += " → " + ui.Clean(c.Fix)
			}
			if c.Status == doctor.Fail {
				warn.Error("doctor: %s", line)
			} else {
				warn.Warn("doctor: %s", line)
			}
		}
	}
	// The last lines say whether anything needs the person, which is what a terminal shows last.
	var summary []string
	switch {
	case problems == 0:
		summary = append(summary, "aw doctor found nothing wrong.")
	case problems == 1:
		summary = append(summary, "aw doctor found 1 problem, above.")
	default:
		summary = append(summary, fmt.Sprintf("aw doctor found %d problems, above.", problems))
	}
	if installFailed != nil {
		summary = append(summary, "Not set up: "+ui.Clean(firstLine(installFailed.Error()))+" → fix it, then run aw setup again.")
	}
	if finish == nil {
		summary = append(summary, "Set up. Next: aw new \"<purpose>\", or start a harness here.")
	}
	ui.NewOut(out).Summary(summary...)
	return finish
}

// repoName names a repository the way the rest of aw's output does: by its project's name, or as
// the workspace repository when no project lives at the workspace root.
func repoName(c *config.Config, dir string) string {
	for _, name := range slices.Sorted(maps.Keys(c.Projects)) {
		if filepath.Clean(c.Projects[name].Directory) == filepath.Clean(dir) {
			return name
		}
	}
	if dir == "." {
		return "the workspace repository"
	}
	return dir
}
