package procs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

// A keeper (S8) is a host process that stays attached to a command in a container. The container's
// PIDs may not be visible from the host (macOS), so the keeper is what the host tracks: its PID is the
// holder, and stopping it stops the command's process group in the container.

// wrapper runs in the container. It reports its PID and start time, then becomes the command, which
// docker exec has made a session and process-group leader. A container always has /proc; without one
// the start time is 0, and the identity is unknown.
const wrapper = `start() { shift 19; echo "$1"; }
s=0
if [ -r /proc/$$/stat ] && read -r l < /proc/$$/stat; then r=${l##*) }; s=$(start $r); fi
printf 'aw-keeper %s %s\n' "$$" "$s"
exec "$@"`

// KeepSpec is what a keeper runs.
type KeepSpec struct {
	Exec      []string `json:"exec"` // docker's arguments, up to and including the container
	Container string   `json:"container"`
	User      string   `json:"user"`
	Argv      []string `json:"argv"`
	Record    string   `json:"record"` // where to record the group, so it can be stopped if the keeper dies; "" for no record
}

// record is the file a keeper leaves while its command runs.
type record struct {
	Container string `json:"container"`
	User      string `json:"user"`
	PID       int    `json:"pid"`
	Start     string `json:"start"`
}

// RecordPath is where the keeper of a command logging to log records its process group.
func RecordPath(log string) string { return log + ".container" }

func dockerExec(ctx context.Context, container, user, script string) error {
	args := []string{"exec"}
	if user != "" {
		args = append(args, "-u", user)
	}
	return exec.CommandContext(ctx, "docker", append(args, container, "sh", "-c", script)...).Run()
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGHUP:
		return "HUP"
	case syscall.SIGKILL:
		return "KILL"
	}
	return "TERM"
}

// Keep runs a command in a container and stays attached until it ends, passing on signals. It
// returns the command's exit status.
func Keep(s KeepSpec, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	c := exec.Command("docker", append(append(append([]string{}, s.Exec...), "sh", "-c", wrapper, "aw-keeper"), s.Argv...)...)
	c.Stdin, c.Stderr = stdin, stderr
	// In its own group: a signal to the keeper must not kill the client, which would leave the
	// command running with nobody attached. Input from a terminal needs the foreground group, and
	// there Ctrl-C reaches both, so the forward below is waited for.
	if f, ok := stdin.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	pipe, err := c.StdoutPipe()
	if err != nil {
		return 1, err
	}
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := c.Start(); err != nil {
		return 127, fmt.Errorf("docker: %w → install Docker, or set the project's runner to host", err)
	}
	identified := make(chan record, 1)
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		r := bufio.NewReader(pipe)
		line, err := r.ReadString('\n')
		f := strings.Fields(line)
		rec := record{Container: s.Container, User: s.User}
		if err == nil && len(f) == 3 && f[0] == "aw-keeper" {
			rec.PID, _ = strconv.Atoi(f[1])
			rec.Start = f[2]
			if s.Record != "" {
				writeRecord(s.Record, rec)
			}
		} else {
			io.WriteString(stdout, line)
		}
		identified <- rec
		io.Copy(stdout, r)
	}()
	waited := make(chan struct{})
	var forwarding sync.WaitGroup
	go func() {
		rec := <-identified
		for {
			select {
			case sig := <-sigs:
				name := signalName(sig.(syscall.Signal))
				if rec.PID == 0 {
					c.Process.Signal(sig)
					continue
				}
				// The client is attached, so this PID is still the command's: no need to verify it.
				forwarding.Add(1)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = dockerExec(ctx, s.Container, s.User, fmt.Sprintf("kill -%[1]s -%[2]d 2>/dev/null || kill -%[1]s %[2]d", name, rec.PID))
				cancel()
				forwarding.Done()
			case <-waited:
				return
			}
		}
	}()
	<-copied
	err = c.Wait()
	close(waited)
	forwarding.Wait()
	if s.Record != "" {
		os.Remove(s.Record)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return ee.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func writeRecord(path string, r record) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		os.Rename(tmp, path)
	}
}

// inContainer is a signal to a recorded group, sent only if the recorded process is still the one
// that was started. It exits 3 when it is gone.
func inContainer(r record, sig string) string {
	return fmt.Sprintf(`start() { shift 19; echo "$1"; }
read -r l < /proc/%[1]d/stat 2>/dev/null || exit 3
r=${l##*) }
[ "$(start $r)" = %[2]q ] || exit 3
kill -%[3]s -%[1]d 2>/dev/null || kill -%[3]s %[1]d`, r.PID, r.Start, sig)
}

// Reap stops a container process group whose keeper is no longer attached, from the keeper's record.
// With no record there is nothing to do. An identity that cannot be confirmed is left alone.
func Reap(log string, grace time.Duration) {
	if log == "" {
		return
	}
	path := RecordPath(log)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	defer os.Remove(path)
	var r record
	if json.Unmarshal(data, &r) != nil || r.PID <= 1 || r.Start == "0" || r.Start == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace+10*time.Second)
	defer cancel()
	if dockerExec(ctx, r.Container, r.User, inContainer(r, "TERM")) != nil {
		return // gone already, or the container is
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if dockerExec(ctx, r.Container, r.User, inContainer(r, "0")) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = dockerExec(ctx, r.Container, r.User, inContainer(r, "KILL"))
}
