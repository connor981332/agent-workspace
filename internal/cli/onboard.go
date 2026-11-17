package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/lanes"
	"github.com/connor981332/agent-workspace/internal/lifecycle"
	"github.com/connor981332/agent-workspace/internal/sandbox"
	"github.com/connor981332/agent-workspace/internal/ui"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// cloneProjects clones the project repositories that are not here yet. Git always runs on the host.
func cloneProjects(ctx context.Context, owner string, c *config.Config, out, stderr io.Writer) error {
	names := make([]string, 0, len(c.Projects))
	for name := range c.Projects {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p := c.Projects[name]
		dir := filepath.Join(owner, p.Directory)
		if p.Directory == "." {
			continue
		}
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if p.Repo == "" {
			fmt.Fprintf(stderr, "warning: project %s is not cloned, and has no repo in workspace.toml → clone it to %s, or set [projects.%s] repo\n", name, ui.Clean(dir), name)
			continue
		}
		step := ui.NewOut(out).Step("Cloning " + p.Repo)
		var said bytes.Buffer
		clone := exec.CommandContext(ctx, "git", "clone", "--", p.Repo, dir)
		clone.Stdout, clone.Stderr = &said, &said
		if err := clone.Run(); err != nil {
			step.Fail(err.Error())
			fmt.Fprint(stderr, said.String())
			return fmt.Errorf("cloning %s failed: %w → check that you have access to it, then run aw setup again", name, err)
		}
		step.Done("into " + p.Directory)
	}
	return nil
}

var knownHarnesses = []string{"claude", "codex", "grok", "opencode"}

var harnessWord = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// ensureIndividual creates the individual layer, asking for the default harness. A file that exists
// is the person's own, and is left alone.
func ensureIndividual(c *config.Config, harness string, in io.Reader, out io.Writer) error {
	path := config.IndividualFile(c.Workspace.Namespace)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	installed := slices.DeleteFunc(slices.Clone(knownHarnesses), func(h string) bool { _, err := exec.LookPath(h); return err != nil })
	suggested := "claude"
	if len(installed) > 0 {
		suggested = installed[0]
	}
	if harness == "" {
		harness = suggested
		if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			fmt.Fprintf(out, "Which harness should aw new start? Found: %s [%s] ", strings.Join(installed, ", "), suggested)
			line, _ := bufio.NewReader(in).ReadString('\n')
			if v := strings.TrimSpace(line); v != "" {
				harness = v
			}
		}
	}
	if !harnessWord.MatchString(harness) {
		return fmt.Errorf("%q is not a harness name → name the harness's command, for example claude", harness)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	text := fmt.Sprintf("# Your own settings for this workspace. Git does not see this file.\n\n[user]\nharness = %q\n", harness)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s\n", ui.Clean(path))
	return nil
}

// reportSecrets says which secret files are missing: they are not in Git, so nothing else can.
func reportSecrets(owner string, c *config.Config, stderr io.Writer) {
	for name, p := range c.Projects {
		for _, s := range p.Secrets {
			if _, err := os.Stat(filepath.Join(owner, p.Directory, s)); err != nil {
				fmt.Fprintf(stderr, "warning: %s's secret file %s is missing, and lanes link it from the owner checkout → get it from your team, and put it at %s\n", name, ui.Clean(s), ui.Clean(filepath.Join(p.Directory, s)))
			}
		}
	}
}

// setupSandboxes keeps the sandbox files out of Git and writes the owner checkout's.
func setupSandboxes(ws *workspace.Workspace, c *config.Config) ([]sandbox.Result, error) {
	if err := sandbox.Exclude(ws.Owner); err != nil {
		return nil, err
	}
	m := &lanes.Manager{WS: ws, Cfg: c}
	return sandbox.Install(ws.Owner, m.SandboxRoots(""))
}

// installOwner installs dependencies in the owner checkout, skipping those whose stamp has not
// changed. A project in a container needs its container, which starts only if the human says so
// (R49). One failure does not stop the rest, and the first is returned.
func installOwner(cmd *cobra.Command, ws *workspace.Workspace, c *config.Config, startContainers, quiet bool) error {
	ctx, out, errOut := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if quiet {
		out = errOut
	}
	w, err := openForWrite()
	if err != nil {
		return err
	}
	defer w.Close()
	steps := ui.NewOut(out)
	defer steps.Close()
	// The run's own log holds every step and detail; the newest 50 runs, a week at most, are kept.
	runLog := steps.LogTo(filepath.Join(filepath.Dir(w.store.Path()), "logs", "runs"), "setup", 50, 7*24*time.Hour)
	m := manager(w)
	var containers []string
	names := make([]string, 0, len(c.Projects))
	for name, p := range c.Projects {
		names = append(names, name)
		if p.Runner == "devcontainer" {
			containers = append(containers, name)
		}
	}
	slices.Sort(names)
	slices.Sort(containers)
	if len(containers) > 0 && !startContainers {
		startContainers = confirm(cmd.InOrStdin(), out, fmt.Sprintf("Start the containers for %s now?", strings.Join(containers, ", ")))
	}
	stateDir := filepath.Dir(w.store.Path())
	infra := infraOf(ws, c)
	if infra.Configured() && len(containers) == 0 && !startContainers {
		startContainers = confirm(cmd.InOrStdin(), out, "Start the shared infrastructure now?")
	}
	if infra.Configured() && startContainers {
		if err := infra.Up(ctx, errOut); err != nil {
			fmt.Fprintf(errOut, "warning: the shared infrastructure did not start: %v\n", err)
		}
	}
	var first error
	fail := func(project string, err error) {
		fmt.Fprintf(errOut, "warning: %s did not install: %v\n", project, err)
		if first == nil {
			first = fmt.Errorf("%s did not install: %w", project, err)
		}
	}
	for _, name := range names {
		p := c.Projects[name]
		inContainer := p.Runner == "devcontainer"
		if inContainer && startContainers {
			d, err := devcontainerOf(w, name)
			if err == nil {
				_, err = d.Up(ctx, errOut)
			}
			if err != nil {
				fail(name, err)
				continue
			}
		}
		if p.Install == "" {
			continue
		}
		if inContainer && !startContainers {
			fmt.Fprintf(out, "%s installs in its container, which is not started → run aw setup --containers when you want it\n", name)
			continue
		}
		where, err := m.LocateIn(0, name)
		if err != nil {
			fail(name, err)
			continue
		}
		stampPath := filepath.Join(stateDir, "owner", name+".install-stamp")
		stamp := lifecycle.InstallStamp(p.Install, p.InstallStamp, where.Dir)
		if have, err := os.ReadFile(stampPath); err == nil && string(have) == stamp {
			continue
		}
		log := filepath.Join(stateDir, "owner", "logs", "setup", name+".install.log")
		env := append(append([]string{}, where.Env...), lifecycle.HookEnv(lifecycle.PhaseInstall, false)...)
		argv, err := hookInContainer(ctx, w, setupLane{}, name, where.Dir, env, p.Install, log)
		if err != nil {
			fail(name, err)
			continue
		}
		step := steps.Step(name + ": installing dependencies")
		stop := followLog(steps, step, log)
		_, err = lifecycle.Exec(ctx, lifecycle.Command{Dir: where.Dir, Env: where.Env, Run: p.Install, Argv: argv, Log: log, Phase: lifecycle.PhaseInstall})
		stop()
		if err != nil {
			step.Fail(fmt.Sprintf("%v (full log: %s)", err, log))
			why := fmt.Sprintf("%v (full log: %s)", err, log)
			if t := lifecycle.Tail(log); t != "" {
				why += "\nThe end of its log:\n" + t
			}
			fail(name, errors.New(why))
			continue
		}
		step.Done("done")
		if err := os.WriteFile(stampPath, []byte(stamp), 0o600); err != nil {
			fail(name, err)
		}
	}
	if first != nil && runLog != "" {
		steps.Line("Everything this setup did is in %s.", runLog)
	}
	return first
}

// followLog shows, on a terminal, the last line a step's command wrote to its log, so that a long
// install shows what it is doing rather than a bare spinner. Elsewhere the step's own "still
// running" lines are enough, and the log has the rest.
func followLog(o *ui.Out, s *ui.Step, log string) (stop func()) {
	if !o.Live() {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if line := lastLine(log); line != "" {
					s.Detail(line)
				}
			}
		}
	}()
	return func() { close(done) }
}

// lastLine is the last non-empty line of a file, reading only its end.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	if info, err := f.Stat(); err == nil && info.Size() > int64(len(buf)) {
		f.Seek(info.Size()-int64(len(buf)), io.SeekStart)
	}
	n, _ := io.ReadFull(f, buf)
	lines := strings.Split(strings.TrimRight(string(buf[:n]), "\n\r "), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func confirm(in io.Reader, out io.Writer, question string) bool {
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false
	}
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}
