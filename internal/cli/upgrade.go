package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/connor981332/agent-workspace/internal/app"
	"github.com/connor981332/agent-workspace/internal/compose"
	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/doctor"
	"github.com/connor981332/agent-workspace/internal/launcher"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/upgrade"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

const upgradeFinish = "__upgrade-finish"

// readPin returns the owner checkout's pinned version.
func readPin(owner string) (string, error) {
	data, err := os.ReadFile(filepath.Join(owner, launcher.VersionPath))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func newUpgradeCmd() *cobra.Command {
	var to string
	var eject, take []string
	cmd := &cobra.Command{
		Use:   "upgrade [--to version] [--eject file]… [--take file]…",
		Short: "Move this workspace to another release of aw, for a merge request to review",
		Long: `Move the workspace to a release: show the changelog, rewrite .aw/version and .aw/lock, migrate
workspace.toml, regenerate managed files, and run aw doctor. Run it on a branch in the owner
checkout, and commit the result: that merge request is the human review of the change.

A managed file edited by hand is never overwritten. It is listed, and you choose: --eject keeps the
edit and stops aw managing the file, --take replaces it with the new version. Running this again at
the same version applies those choices.`,
		Args: cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			ws, _, err := loadConfig()
			if err != nil {
				return err
			}
			if ws.LaneRoot != "" {
				return fmt.Errorf("aw upgrade changes the owner checkout's pin, and this is a lane → run it in %s", ui.Clean(ws.Owner))
			}
			from, err := readPin(ws.Owner)
			if err != nil {
				return fmt.Errorf("reading the pin: %w → restore .aw/version from Git", err)
			}
			target := strings.TrimPrefix(to, "v")
			if target == "" {
				if target, err = launcher.Latest(); err != nil {
					return err
				}
			}
			if !launcher.Exact(target) {
				return &usageError{fmt.Errorf("%q is not an exact version → name a release such as 1.4.2", target)}
			}
			if upgrade.Compare(target, from) < 0 {
				return fmt.Errorf("aw %s is older than the pinned %s, and aw upgrade only goes forward → to go back, restore .aw/version and .aw/lock from Git", target, from)
			}
			if target != from {
				if err := movePin(cmd, ws.Owner, from, target); err != nil {
					return err
				}
			}
			// The new version does the rest: only it knows its own configuration schema and
			// managed files.
			script := filepath.Join(ws.Owner, launcher.ScriptPath)
			args := []string{script, upgradeFinish, "--from", from}
			for _, f := range eject {
				args = append(args, "--eject", f)
			}
			for _, f := range take {
				args = append(args, "--take", f)
			}
			return syscall.Exec(script, args, os.Environ())
		}),
	}
	cmd.Flags().StringVar(&to, "to", "", "the release to move to (default: the latest)")
	cmd.Flags().StringArrayVar(&eject, "eject", nil, "keep this hand-edited managed file, and stop aw managing it")
	cmd.Flags().StringArrayVar(&take, "take", nil, "replace this hand-edited managed file with the new version")
	return cmd
}

// movePin rewrites the pin and has the launcher fetch and verify the new binary. If that fails the
// pin goes back, so that a failed download leaves the workspace as it was.
func movePin(cmd *cobra.Command, owner, from, target string) error {
	lock, platforms, err := launcher.Lock(launcher.Mirror(target), target)
	if err != nil {
		return err
	}
	if len(platforms) < len(launcher.Platforms) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: aw %s has binaries for only %s\n", target, strings.Join(platforms, ", "))
	}
	versionPath, lockPath := filepath.Join(owner, launcher.VersionPath), filepath.Join(owner, launcher.LockPath)
	oldVersion, err := os.ReadFile(versionPath)
	if err != nil {
		return err
	}
	oldLock, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	restore := func() {
		os.WriteFile(versionPath, oldVersion, 0o644)
		os.WriteFile(lockPath, oldLock, 0o644)
	}
	if err := os.WriteFile(versionPath, []byte(target+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(lockPath, lock, 0o644); err != nil {
		restore()
		return err
	}
	fetch := exec.Command(filepath.Join(owner, launcher.ScriptPath), "version")
	fetch.Stderr = cmd.ErrOrStderr()
	if err := fetch.Run(); err != nil {
		restore()
		return fmt.Errorf("could not fetch and verify aw %s (%v), so the workspace stays on %s", target, err, from)
	}
	return nil
}

func newUpgradeFinishCmd() *cobra.Command {
	var from string
	var eject, take []string
	cmd := &cobra.Command{
		Use:    upgradeFinish,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			return finishUpgrade(cmd, from, eject, take)
		}),
	}
	cmd.Flags().StringVar(&from, "from", "", "")
	cmd.Flags().StringArrayVar(&eject, "eject", nil, "")
	cmd.Flags().StringArrayVar(&take, "take", nil, "")
	return cmd
}

// finishUpgrade is run by the new version, once the pin names it.
func finishUpgrade(cmd *cobra.Command, from string, eject, take []string) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	to := frameworkVersion()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ws, err := workspace.Find(cwd)
	if err != nil {
		return err
	}
	if from != to {
		fmt.Fprintf(out, "aw %s → %s\n", ui.Clean(from), to)
		notes, breaking := upgrade.Changes(from, to)
		if notes != "" {
			fmt.Fprintln(out, notes)
		}
		for _, b := range breaking {
			fmt.Fprintf(errOut, "breaking: %s\n", b)
		}
	}

	// The configuration comes first: everything else reads it.
	if err := migrateConfig(cmd, ws.Owner); err != nil {
		return err
	}
	ws, c, err := loadConfig()
	if err != nil {
		return err
	}
	// The first command of a new version migrates the state, after backing it up.
	w, err := openForWrite()
	if err != nil {
		return err
	}
	w.Close()

	edited, err := regenerate(cmd, ws.Owner, c, eject, take)
	if err != nil {
		return err
	}
	report := doctor.Run(ws.Owner, to, false)
	for _, check := range report.Checks {
		if check.Status == doctor.Warn || check.Status == doctor.Fail {
			fmt.Fprintf(errOut, "doctor: %s: %s\n", check.Name, ui.Clean(check.Message))
		}
	}
	if len(edited) > 0 {
		for _, f := range edited {
			fmt.Fprintf(errOut, "%s was edited by hand, so aw left it alone → keep the edit with aw upgrade --to %s --eject %s, or take the new version with --take %s\n", f, to, f, f)
		}
		return &silentError{errors.New("managed files need a decision")}
	}
	if !report.OK {
		return &silentError{errors.New("aw doctor found problems")}
	}
	fmt.Fprintln(out, "Next: commit this, and open a merge request. Everyone else gets it with git pull.")
	return nil
}

// migrateConfig brings workspace.toml to this binary's schema and shows what changed.
func migrateConfig(cmd *cobra.Command, owner string) error {
	path := filepath.Join(owner, config.TeamFile)
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, err := upgrade.Migrate(string(old), config.SchemaVersion)
	if err != nil {
		return err
	}
	if next == string(old) {
		return nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return err
	}
	diff := exec.CommandContext(context.Background(), "git", "-C", owner, "--no-pager", "diff", "--no-color", "--", config.TeamFile)
	diff.Stdout, diff.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	_ = diff.Run() // it only shows what is already done
	return nil
}

// regenerate refreshes the managed files, and returns those left alone because of a hand edit.
func regenerate(cmd *cobra.Command, owner string, c *config.Config, eject, take []string) ([]string, error) {
	for _, f := range append(slices.Clone(eject), take...) {
		if f != launcher.ScriptPath {
			return nil, &usageError{fmt.Errorf("%s is not a managed file that aw upgrade can eject or replace → the one is %s; a generated AGENTS.md block is fixed by moving the edit to .aw/instructions.md", f, launcher.ScriptPath)}
		}
	}
	var edited []string
	path := filepath.Join(owner, launcher.ScriptPath)
	recorded := upgrade.Recorded(owner, launcher.ScriptPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch {
	case slices.Contains(eject, launcher.ScriptPath):
		if err := upgrade.Record(owner, launcher.ScriptPath, ""); err != nil {
			return nil, err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is yours now: aw will not manage it\n", launcher.ScriptPath)
	case recorded == "":
		// Ejected earlier: it stays the team's.
	case upgrade.Hash(data) != recorded && !slices.Contains(take, launcher.ScriptPath):
		edited = append(edited, launcher.ScriptPath)
	default:
		if err := os.WriteFile(path, launcher.Script, 0o755); err != nil {
			return nil, err
		}
		if err := upgrade.Record(owner, launcher.ScriptPath, upgrade.Hash(launcher.Script)); err != nil {
			return nil, err
		}
	}
	if _, err := app.InstallGitHooks(cmd.Context(), owner, c); err != nil {
		return nil, err
	}
	if _, err := app.InstallHarnessConfig(owner); err != nil {
		return nil, err
	}
	instructions, err := compose.Apply(cmd.Context(), owner, c.Workspace.Name, c.Workspace.Namespace)
	if err != nil {
		return nil, err
	}
	for _, r := range instructions {
		if r.Edited {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", r.Note)
		}
	}
	return edited, nil
}
