// Package lifecycle runs a lane's setup steps (lifecycle.md, "Lifecycle hooks"): the workspace's and
// each project's prepare, each project's install (skipped while its stamp is unchanged), and setup,
// or reset for a recycled lane. Every step is journaled. Projects run in parallel.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/gitx/run"
	"github.com/connor981332/agent-workspace/internal/platform"
	"github.com/connor981332/agent-workspace/internal/procs"
	"github.com/connor981332/agent-workspace/internal/state"
	"github.com/connor981332/agent-workspace/internal/ui"
)

// Phases, as hooks see them in AW_PHASE.
const (
	PhaseData     = "data" // not a hook: a provider creates or resets the lane's data
	PhasePrepare  = "prepare"
	PhaseInstall  = "install"
	PhaseCheckout = "post-checkout" // the repository's own hook, which creating the worktree skips
	PhaseSetup    = "setup"
	PhaseReset    = "reset"
	PhaseTeardown = "teardown"
	PhaseRestore  = "restore"
)

// StartWindow is how long a setup may be recorded with no process before it counts as lost.
const StartWindow = 30 * time.Second

// killGrace is how long a stopped step has between SIGTERM and SIGKILL.
const killGrace = 5 * time.Second

// ErrRunning says another process is already running the lane's setup.
var ErrRunning = errors.New("setup is already running")

// Step is one step of a plan.
type Step struct {
	Name    string // prepare, or <project>.<phase>
	Phase   string
	Project string // "" for the workspace's
	Hook    *config.Hook
	Stamp   []string // install: the files whose contents decide whether it reruns
	Data    string   // data: the [lanes.data] block it provisions
}

// CheckoutHooks finds each project's own post-checkout hook, by project. Creating a lane's worktree
// skips it, so it runs as a setup step: a failure leaves a lane that can be looked at and retried.
func CheckoutHooks(ctx context.Context, owner string, c *config.Config) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, p := range c.Projects {
		if p.Lanes == "shared" || p.PostCheckout == "skip" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h := run.HookPath(ctx, filepath.Join(owner, p.Directory), "post-checkout"); h != "" {
				mu.Lock()
				out[name] = h
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// Plan returns a lane's setup steps, in order: the data, what is not specific to one project, and
// then each project's. fromPool picks reset over setup. hooks are the projects' post-checkout hooks.
func Plan(c *config.Config, fromPool bool, hooks map[string]string) []Step {
	var out []Step
	data := make([]string, 0, len(c.Lanes.Data))
	for name := range c.Lanes.Data {
		data = append(data, name)
	}
	slices.Sort(data)
	for _, name := range data {
		out = append(out, Step{Name: "data." + name, Phase: PhaseData, Data: name})
	}
	if c.Lifecycle.Prepare != nil {
		out = append(out, Step{Name: PhasePrepare, Phase: PhasePrepare, Hook: c.Lifecycle.Prepare})
	}
	projects := make([]string, 0, len(c.Projects))
	for name := range c.Projects {
		projects = append(projects, name)
	}
	slices.Sort(projects)
	for _, name := range projects {
		if h := c.Projects[name].Lifecycle.Prepare; h != nil {
			out = append(out, Step{Name: name + "." + PhasePrepare, Phase: PhasePrepare, Project: name, Hook: h})
		}
	}
	for _, name := range projects {
		p := c.Projects[name]
		// A shared project's lanes run the owner's checkout, whose dependencies are the owner's.
		if p.Install != "" && p.Lanes != "shared" {
			out = append(out, Step{Name: name + "." + PhaseInstall, Phase: PhaseInstall, Project: name,
				Hook: &config.Hook{Run: p.Install}, Stamp: p.InstallStamp})
		}
		if h := hooks[name]; h != "" {
			out = append(out, Step{Name: name + "." + PhaseCheckout, Phase: PhaseCheckout, Project: name, Hook: &config.Hook{Run: checkoutCommand(h)}})
		}
		phase, h := PhaseSetup, p.Lifecycle.Setup
		if fromPool {
			phase, h = PhaseReset, p.Lifecycle.Reset
		}
		if h != nil {
			out = append(out, Step{Name: name + "." + phase, Phase: phase, Project: name, Hook: h})
		}
	}
	return out
}

// checkoutCommand runs a post-checkout hook as Git would after creating a worktree.
func checkoutCommand(hook string) string {
	return "exec '" + strings.ReplaceAll(hook, "'", `'\''`) + `' 0000000000000000000000000000000000000000 "$(git rev-parse HEAD)" 1`
}

// Names returns the names of a plan's steps.
func Names(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

// Needed returns the steps a command in the project waits for (S12): everything not specific to one
// project, the project's own steps, and those it names in after.
func Needed(project string, after []string) func(string) bool {
	return func(step string) bool {
		if step == PhasePrepare || strings.HasPrefix(step, "data.") || slices.Contains(after, step) {
			return true
		}
		return project != "" && strings.HasPrefix(step, project+".")
	}
}

// Incomplete is the error for a lane whose setup failed. It names the retry command.
func Incomplete(name string, s state.Step) error {
	why := s.Name + " failed"
	if s.Detail != "" {
		why += ": " + s.Detail
	}
	where := ""
	if s.Log != "" {
		where = ", log " + s.Log
	}
	err := fmt.Sprintf("lane %s: setup incomplete (%s%s) → aw setup %s", name, why, where, name)
	if t := Tail(s.Log); t != "" {
		err += "\nThe end of its log:\n" + t
	}
	return errors.New(err)
}

const (
	tailLines = 20
	tailWidth = 300
)

// Tail is the end of a hook's log, for an error: its last lines, none of them long. The whole log
// stays in its file.
func Tail(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	for i, l := range lines {
		l = ui.Clean(l)
		if len(l) > tailWidth {
			l = l[:tailWidth] + "…"
		}
		lines[i] = "  " + l
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \n")
}

func firstFailed(steps []state.Step, want func(string) bool) *state.Step {
	for i := range steps {
		if steps[i].Status == state.StepFailed && want(steps[i].Name) {
			return &steps[i]
		}
	}
	return nil
}

// Await waits for the steps want selects, and refuses when one of them failed. say is told once
// what is being waited for.
func Await(ctx context.Context, store *state.Store, lane state.Lane, want func(string) bool, say func(string)) error {
	said := false
	for {
		steps, err := state.Steps(store.DB(), lane.Number, lane.Generation)
		if err != nil {
			return err
		}
		if f := firstFailed(steps, want); f != nil {
			return Incomplete(lane.Name, *f)
		}
		waiting := ""
		for _, s := range steps {
			if want(s.Name) && !s.Finished() {
				waiting = s.Name
				break
			}
		}
		if waiting == "" {
			return nil
		}
		if !said {
			said = true
			say(waiting)
		}
		if err := Recover(ctx, store); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Recover notices setups whose process has gone (a crash or a reboot): what was running failed, and
// the lane is incomplete, so that a command waiting for it stops waiting and is told the fix.
func Recover(ctx context.Context, store *state.Store) error {
	active, err := state.ActiveSetups(store.DB())
	if err != nil || len(active) == 0 {
		return err
	}
	for _, s := range active {
		if alive(s) || (s.PID == 0 && s.Age() < StartWindow) {
			continue
		}
		s := s
		if err := store.Write(ctx, func(tx *sql.Tx) error {
			current, err := state.LaneSetup(tx, s.Lane)
			if err != nil || current.PID != s.PID || current.PIDStart != s.PIDStart {
				return err
			}
			if err := state.FailRunningSteps(tx, s.Lane, s.Generation, "aw's background setup stopped before it finished"); err != nil {
				return err
			}
			if err := state.SetSetupRunner(tx, s.Lane, 0, ""); err != nil {
				return err
			}
			return setLaneState(tx, s.Lane, s.Generation, state.LaneIncomplete)
		}); err != nil {
			return err
		}
	}
	return nil
}

func alive(s state.Setup) bool {
	if s.PID == 0 {
		return false
	}
	start, err := platform.ProcessIdentity(s.PID)
	return err == nil && start == s.PIDStart
}

// setLaneState moves a lane out of, or back into, setting up. A lane that has moved on (it was
// parked or finished meanwhile) keeps its state.
func setLaneState(tx *sql.Tx, lane, generation int, to string) error {
	l, err := state.LaneByNumber(tx, lane)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	if l.Generation != generation || (l.State != state.LaneSettingUp && l.State != state.LaneIncomplete) {
		return nil
	}
	l.State, l.Updated = to, state.Now()
	return state.UpdateLane(tx, l)
}

// Stop ends the setup running in a lane and waits for it to go, before the lane is destroyed.
func Stop(ctx context.Context, store *state.Store, lane int) {
	s, err := state.LaneSetup(store.DB(), lane)
	if err != nil || !alive(s) || s.PID == os.Getpid() {
		return
	}
	syscall.Kill(s.PID, syscall.SIGTERM)
	deadline := time.Now().Add(killGrace + 2*time.Second)
	for alive(s) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Runner runs one lane's setup.
type Runner struct {
	Store    *state.Store
	Cfg      *config.Config
	Lane     state.Lane
	StateDir string // the lane's private state directory
	// Fresh is AW_FRESH: the lane's data was just created or reset.
	Fresh bool
	// Where returns the directory a project's steps run in, and their environment. "" is the lane root.
	Where func(project string) (string, []string, error)
	// Seats takes the resources a step needs, and returns how to give them back.
	Seats func(ctx context.Context, needs []string) (func(), error)
	// Provision creates or resets one data block.
	Provision func(ctx context.Context, data string, fromPool bool) error
	// InContainer returns the command that runs a hook in its project's container, or nil when the
	// project runs on the host. What starting the container says goes to the log. Project-level
	// prepare stays on the host: it writes the env files that commands in the container read.
	InContainer func(ctx context.Context, project, dir string, env []string, run, log string) ([]string, error)
	// Hooks are the projects' post-checkout hooks, from CheckoutHooks.
	Hooks map[string]string
	// After runs once the steps have, even if one failed. Its failure is reported, not fatal.
	After func(ctx context.Context) error
	// Out is told about each step. It may be nil.
	Out io.Writer
}

func (r *Runner) say(format string, args ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, "aw: "+format+"\n", args...)
	}
}

// Run runs what is left of the lane's setup, and leaves the lane working or incomplete. The returned
// error names the retry command when a step failed.
func (r *Runner) Run(ctx context.Context) error {
	done, err := r.begin(ctx)
	if err != nil {
		return err
	}
	steps := Plan(r.Cfg, done.fromPool, r.Hooks)
	var serial []Step
	projects := map[string][]Step{}
	for _, s := range steps {
		if s.Project == "" || s.Phase == PhasePrepare {
			serial = append(serial, s)
		} else {
			projects[s.Project] = append(projects[s.Project], s)
		}
	}
	ok := true
	for _, s := range serial {
		if !r.step(ctx, s, done) {
			ok = false
			break
		}
	}
	if ok {
		var wg sync.WaitGroup
		for _, list := range projects {
			wg.Add(1)
			go func(list []Step) {
				defer wg.Done()
				for _, s := range list {
					if !r.step(ctx, s, done) {
						return
					}
				}
			}(list)
		}
		wg.Wait()
	}
	if r.After != nil {
		if err := r.After(context.Background()); err != nil {
			r.say("warning: %v", err)
		}
	}
	return r.finish()
}

type begun struct {
	fromPool bool
	finished map[string]bool
}

// begin records the plan and takes the lane's setup for this process.
func (r *Runner) begin(ctx context.Context) (*begun, error) {
	pid := os.Getpid()
	start, err := platform.ProcessIdentity(pid)
	if err != nil {
		return nil, err
	}
	b := &begun{finished: map[string]bool{}}
	err = r.Store.Write(ctx, func(tx *sql.Tx) error {
		if s, err := state.LaneSetup(tx, r.Lane.Number); err == nil {
			if alive(s) && s.PID != pid {
				return ErrRunning
			}
			b.fromPool = s.FromPool && s.Generation == r.Lane.Generation
		} else if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err := state.PlanSetup(tx, r.Lane.Number, r.Lane.Generation, b.fromPool, Names(Plan(r.Cfg, b.fromPool, r.Hooks))); err != nil {
			return err
		}
		if err := state.SetSetupRunner(tx, r.Lane.Number, pid, start); err != nil {
			return err
		}
		steps, err := state.Steps(tx, r.Lane.Number, r.Lane.Generation)
		if err != nil {
			return err
		}
		for _, s := range steps {
			if s.Finished() {
				b.finished[s.Name] = true
			}
		}
		return setLaneState(tx, r.Lane.Number, r.Lane.Generation, state.LaneSettingUp)
	})
	return b, err
}

// step runs one step, and reports whether the steps after it may run.
func (r *Runner) step(ctx context.Context, s Step, b *begun) bool {
	if b.finished[s.Name] {
		return true
	}
	log := filepath.Join(r.StateDir, "logs", "setup", s.Name+".log")
	end := func(status, detail string, exit *int) bool {
		// Recording the end must survive a stop: the context may be why the step ended.
		if err := r.Store.Write(context.Background(), func(tx *sql.Tx) error {
			return state.EndStep(tx, r.Lane.Number, r.Lane.Generation, s.Name, status, detail, exit)
		}); err != nil {
			r.say("%s: could not record its end: %v", s.Name, err)
			return false
		}
		return status == state.StepDone || status == state.StepSkipped
	}
	if err := r.Store.Write(ctx, func(tx *sql.Tx) error {
		return state.StartStep(tx, r.Lane.Number, r.Lane.Generation, s.Name, log)
	}); err != nil {
		return end(state.StepFailed, err.Error(), nil)
	}
	if ctx.Err() != nil {
		return end(state.StepFailed, "stopped before it ran", nil)
	}
	if s.Phase == PhaseData {
		r.say("%s: running", s.Name)
		if err := r.Provision(ctx, s.Data, b.fromPool); err != nil {
			r.say("%s: %v", s.Name, err)
			return end(state.StepFailed, err.Error(), nil)
		}
		r.say("%s: done", s.Name)
		return end(state.StepDone, "", nil)
	}
	dir, env, err := r.Where(s.Project)
	if err != nil {
		return end(state.StepFailed, err.Error(), nil)
	}
	stampPath, stamp := "", ""
	if s.Phase == PhaseInstall {
		stampPath = filepath.Join(r.StateDir, s.Project+".install-stamp")
		stamp = InstallStamp(s.Hook.Run, s.Stamp, dir)
		if have, err := os.ReadFile(stampPath); err == nil && string(have) == stamp {
			r.say("%s: unchanged, skipped", s.Name)
			return end(state.StepSkipped, "its stamp is unchanged", nil)
		}
	}
	release := func() {}
	if len(s.Hook.Needs) > 0 {
		r.say("%s: waiting for %s", s.Name, strings.Join(s.Hook.Needs, ", "))
		if release, err = r.Seats(ctx, s.Hook.Needs); err != nil {
			return end(state.StepFailed, err.Error(), nil)
		}
		// The seats' variables reach the step: where it runs is worked out once it holds them.
		if dir, env, err = r.Where(s.Project); err != nil {
			release()
			return end(state.StepFailed, err.Error(), nil)
		}
	}
	defer release()
	r.say("%s: running", s.Name)
	var argv []string
	if r.InContainer != nil && s.Project != "" && s.Phase != PhasePrepare && s.Phase != PhaseCheckout {
		if argv, err = r.InContainer(ctx, s.Project, dir, append(append([]string{}, env...), HookEnv(s.Phase, r.Fresh)...), s.Hook.Run, log); err != nil {
			r.say("%s: %v", s.Name, err)
			return end(state.StepFailed, err.Error(), nil)
		}
	}
	code, err := Exec(ctx, Command{Dir: dir, Env: env, Run: s.Hook.Run, Argv: argv, Timeout: time.Duration(s.Hook.Timeout), Log: log, Phase: s.Phase, Fresh: r.Fresh})
	if err != nil {
		var c *int
		if code != 0 {
			c = &code
		}
		r.say("%s: %v", s.Name, err)
		return end(state.StepFailed, err.Error(), c)
	}
	if stampPath != "" {
		if err := os.WriteFile(stampPath, []byte(stamp), 0o600); err != nil {
			return end(state.StepFailed, fmt.Sprintf("recording its stamp: %v", err), &code)
		}
	}
	r.say("%s: done", s.Name)
	return end(state.StepDone, "", &code)
}

// InstallStamp is the hash of the install command and the contents of its stamp files, so that a
// checkout reinstalls when either changes. A first install has no stamp to compare with.
func InstallStamp(run string, files []string, dir string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", run)
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00", f)
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			fmt.Fprint(h, "missing\x00")
			continue
		}
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// finish moves the lane on, gives up the setup, and reports the first failure.
func (r *Runner) finish() error {
	var failed *state.Step
	err := r.Store.Write(context.Background(), func(tx *sql.Tx) error {
		steps, err := state.Steps(tx, r.Lane.Number, r.Lane.Generation)
		if err != nil {
			return err
		}
		failed = firstFailed(steps, func(string) bool { return true })
		pending := false
		for _, s := range steps {
			pending = pending || !s.Finished()
		}
		if err := state.SetSetupRunner(tx, r.Lane.Number, 0, ""); err != nil {
			return err
		}
		to := state.LaneWorking
		if failed != nil || pending {
			to = state.LaneIncomplete
		}
		return setLaneState(tx, r.Lane.Number, r.Lane.Generation, to)
	})
	if err != nil {
		return err
	}
	if failed != nil {
		return Incomplete(r.Lane.Name, *failed)
	}
	return nil
}

// Command is one hook to run.
type Command struct {
	Dir string
	Env []string
	Run string
	// Argv replaces Run for a hook in a container: a keeper. Its log is then appended to, after what
	// starting the container wrote.
	Argv    []string
	Timeout time.Duration // 0 means none
	Log     string
	Phase   string
	Fresh   bool
}

// HookEnv is the environment only hooks receive.
func HookEnv(phase string, fresh bool) []string {
	f := "0"
	if fresh {
		f = "1"
	}
	return []string{"AW_PHASE=" + phase, "AW_FRESH=" + f}
}

// Exec runs a hook in its own process group, with its output in the log. It returns the exit status,
// and an error saying why the hook did not succeed.
func Exec(ctx context.Context, c Command) (int, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	if err := os.MkdirAll(filepath.Dir(c.Log), 0o700); err != nil {
		return 0, err
	}
	mode := os.O_TRUNC
	if c.Argv != nil {
		mode = os.O_APPEND
	}
	log, err := os.OpenFile(c.Log, os.O_CREATE|os.O_WRONLY|mode, 0o600)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	cmd := exec.Command("sh", "-c", c.Run)
	if c.Argv != nil {
		cmd = exec.Command(c.Argv[0], c.Argv[1:]...)
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr = c.Dir, log, log
	cmd.Env = append(append(os.Environ(), c.Env...), HookEnv(c.Phase, c.Fresh)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pgid := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var werr error
	stopped := false
	select {
	case werr = <-waited:
	case <-ctx.Done():
		stopped = true
		syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case werr = <-waited:
		case <-time.After(killGrace):
			syscall.Kill(-pgid, syscall.SIGKILL)
			werr = <-waited
		}
	}
	if stopped {
		syscall.Kill(-pgid, syscall.SIGKILL) // anything it left behind
		procs.Reap(c.Log, 0)
	}
	switch {
	case stopped && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return 0, fmt.Errorf("it took longer than its timeout of %s", c.Timeout)
	case stopped:
		return 0, errors.New("it was stopped")
	case werr == nil:
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		return ee.ExitCode(), fmt.Errorf("exit status %d", ee.ExitCode())
	}
	return 0, werr
}
