package runners

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/connor981332/agent-workspace/internal/config"
)

// Fragment is the Compose file aw writes in a project's .devcontainer: the same-path mount and the
// seat ports (S7). It is local to the clone, because it names this checkout's path.
const Fragment = "aw.compose.yaml"

// defaultCLIEnv is what the devcontainer CLI needs by default: Compose 5 builds through buildx bake,
// which refuses the Dockerfile the CLI generates outside the build context (F4).
var defaultCLIEnv = map[string]string{"BUILDX_BAKE_ENTITLEMENTS_FS": "0"}

// Devcontainer is one project's container, shared with VS Code.
type Devcontainer struct {
	Project string
	Dir     string   // the owner's checkout of the project: the workspace folder, and the mount
	Compose string   // the Compose project: the name VS Code derives
	Service string   // the Compose service
	User    string   // the devcontainer's remote user, or ""
	Ports   []int    // every port a seat may use, published when the container is created (R50)
	cli     []string // the devcontainer CLI
	cliEnv  []string
	lock    string // serializes starting the container
}

// devcontainerJSON is what aw reads of devcontainer.json.
type devcontainerJSON struct {
	ComposeFile     json.RawMessage `json:"dockerComposeFile"`
	Service         string          `json:"service"`
	WorkspaceFolder string          `json:"workspaceFolder"`
	RemoteUser      string          `json:"remoteUser"`
	ContainerUser   string          `json:"containerUser"`
	Initialize      json.RawMessage `json:"initializeCommand"`
}

// stripJSONC removes comments and trailing commas, which devcontainer.json allows.
func stripJSONC(b []byte) []byte {
	var out []byte
	var comma = -1 // where in out the last comma is, while only blanks have followed it
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(b) && b[j] != '"' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			end := min(j+1, len(b))
			out = append(out, b[i:end]...)
			i, comma = end-1, -1
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i+1 < len(b) && b[i+1] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			end := bytes.Index(b[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			i += end + 3
		case c == ',':
			comma = len(out)
			out = append(out, c)
		case (c == ']' || c == '}') && comma >= 0:
			out[comma] = ' '
			comma = -1
			out = append(out, c)
		default:
			if !strings.ContainsRune(" \t\r\n", rune(c)) {
				comma = -1
			}
			out = append(out, c)
		}
	}
	return out
}

// ComposeName is the Compose project name VS Code and the devcontainer CLI derive for a folder whose
// Compose file is in .devcontainer (F1).
func ComposeName(dir string) string {
	return strings.TrimLeft(composeUnsafe.ReplaceAllString(strings.ToLower(filepath.Base(dir)+"_devcontainer"), ""), "-_")
}

var composeUnsafe = regexp.MustCompile(`[^-_a-z0-9]`)

// SeatPorts are the ports a project's servers may use: seat 0 to capacity of each.
func SeatPorts(c *config.Config, project string) []int {
	capacity := map[string]int{}
	for _, r := range c.Resources {
		for _, s := range r.Servers {
			capacity[s] = r.Capacity
		}
	}
	var out []int
	for name, s := range c.Projects[project].Servers {
		n, ok := capacity[project+"."+name]
		if !ok {
			n = c.Lanes.ServerCapacity
		}
		for seat := 0; seat <= n; seat++ {
			out = append(out, s.Port+c.Lanes.PortStride*seat+c.Ports.Offset)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func readDevcontainerJSON(dir string) (devcontainerJSON, string, error) {
	var d devcontainerJSON
	name := filepath.Join(dir, ".devcontainer", "devcontainer.json")
	data, err := os.ReadFile(name)
	if err != nil {
		return d, name, fmt.Errorf("%s: %w → add the project's devcontainer.json, or set its runner to host", name, err)
	}
	if err := json.Unmarshal(stripJSONC(data), &d); err != nil {
		return d, name, fmt.Errorf("%s is not valid: %w → fix the file", name, err)
	}
	return d, name, nil
}

// composeFiles returns dockerComposeFile, which is a string or a list.
func (d devcontainerJSON) composeFiles() []string {
	var list []string
	if json.Unmarshal(d.ComposeFile, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(d.ComposeFile, &one) == nil && one != "" {
		return []string{one}
	}
	return nil
}

// Validate checks a devcontainer.json against what the runner needs: Compose, the fragment, and the
// same path inside and outside (S7). Each error names the fix. It only reads the file.
func Validate(dir string) (devcontainerJSON, error) {
	d, name, err := readDevcontainerJSON(dir)
	if err != nil {
		return d, err
	}
	files := d.composeFiles()
	switch {
	case len(files) == 0 || d.Service == "":
		return d, fmt.Errorf(`%s has no dockerComposeFile and service → aw runs one Compose service per project; set "dockerComposeFile" and "service"`, name)
	case !slices.Contains(files, Fragment):
		return d, fmt.Errorf(`%s does not list %s in dockerComposeFile → add it after the project's own Compose file: it publishes the seat ports and makes the same-path mount`, name, Fragment)
	case d.WorkspaceFolder != "${localWorkspaceFolder}":
		return d, fmt.Errorf(`%s sets workspaceFolder to %q → set it to "${localWorkspaceFolder}", so that paths mean the same inside and outside the container`, name, d.WorkspaceFolder)
	}
	return d, nil
}

// ClaimWired reports whether the devcontainer.json runs aw's claim step, which is what makes VS Code
// behave like aw on a moved checkout.
func ClaimWired(d devcontainerJSON, project string) bool {
	return strings.Contains(string(d.Initialize), "containers claim "+project)
}

// CLIPinned reports whether the configured devcontainer CLI command names a version (S16).
func CLIPinned(c config.Devcontainer) bool {
	if strings.Contains(c.Command, "{version}") {
		return c.Version != ""
	}
	return pinnedVersion.MatchString(c.Command)
}

var pinnedVersion = regexp.MustCompile(`@\d+\.\d+\.\d+`)

// NewDevcontainer reads a project's devcontainer. stateDir holds its start lock.
func NewDevcontainer(c *config.Config, owner, project, stateDir string) (*Devcontainer, error) {
	p := c.Projects[project]
	dir := filepath.Join(owner, p.Directory)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	j, err := Validate(dir)
	if err != nil {
		return nil, err
	}
	user := j.RemoteUser
	if user == "" {
		user = j.ContainerUser
	}
	merged := map[string]string{}
	for k, v := range defaultCLIEnv {
		merged[k] = v
	}
	for k, v := range c.Devcontainer.Env {
		merged[k] = v
	}
	var env []string
	for k, v := range merged {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return &Devcontainer{Project: project, Dir: dir, Compose: ComposeName(dir), Service: j.Service, User: user,
		Ports: SeatPorts(c, project), cliEnv: env,
		cli:  strings.Fields(strings.ReplaceAll(c.Devcontainer.Command, "{version}", c.Devcontainer.Version)),
		lock: filepath.Join(stateDir, "containers", project+".lock")}, nil
}

// fragmentText is the fragment. The name pins the Compose project (F1), and the file is rewritten
// before every start, so a moved checkout gets the new path.
func (d *Devcontainer) fragmentText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by aw; do not edit. It names this checkout's path, so it is not committed.\n")
	fmt.Fprintf(&b, "name: %s\nservices:\n  %s:\n    volumes:\n      - type: bind\n        source: %q\n        target: %q\n", d.Compose, d.Service, d.Dir, d.Dir)
	if len(d.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range d.Ports {
			fmt.Fprintf(&b, "      - \"127.0.0.1:%d:%d\"\n", p, p)
		}
	}
	return b.String()
}

// WriteFragment writes the fragment if it changed, and keeps it out of Git through the clone's own
// exclude file, which no commit carries.
func (d *Devcontainer) WriteFragment() error {
	name := filepath.Join(d.Dir, ".devcontainer", Fragment)
	text := d.fragmentText()
	if old, err := os.ReadFile(name); err != nil || string(old) != text {
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			return err
		}
	}
	exclude := filepath.Join(d.Dir, ".git", "info", "exclude")
	line := ".devcontainer/" + Fragment
	old, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(string(old), "\n"), line) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		line = "\n" + line
	}
	_, err = fmt.Fprintln(f, line)
	return err
}

// inspected is what aw reads of docker inspect.
type inspected struct {
	ID    string `json:"Id"`
	State struct {
		Running bool `json:"Running"`
		Pid     int  `json:"Pid"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
}

func docker(ctx context.Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		var missing *exec.Error
		if errors.As(err, &missing) {
			return "", errors.New("docker is not installed or not on PATH → install Docker, or set the project's runner to host")
		}
		return "", fmt.Errorf("docker %s: %s → check that Docker is running", args[0], strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func inspect(ctx context.Context, id string) (inspected, error) {
	var out []inspected
	text, err := docker(ctx, "inspect", id)
	if err != nil {
		return inspected{}, err
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || len(out) != 1 {
		return inspected{}, fmt.Errorf("could not read docker inspect of %s (%v) → check the Docker engine", id, err)
	}
	return out[0], nil
}

// hostPath undoes the prefixes Docker Desktop reports for bind mounts on macOS.
func hostPath(p string) string {
	for _, prefix := range []string{"/host_mnt", "/run/host_mark"} {
		if rest, ok := strings.CutPrefix(p, prefix+"/"); ok {
			p = "/" + rest
		}
	}
	if rest, ok := strings.CutPrefix(p, "/private/"); ok {
		return "/" + rest
	}
	return p
}

func samePath(a, b string) bool { return hostPath(a) == hostPath(b) }

// folder is the host checkout a container belongs to: the devcontainer CLI's label, or failing that
// its same-path mount. "" means nothing says.
func (i inspected) folder() string {
	if f := i.Config.Labels["devcontainer.local_folder"]; f != "" {
		return f
	}
	for _, m := range i.Mounts {
		if m.Type == "bind" && samePath(m.Source, m.Destination) {
			return m.Source
		}
	}
	return ""
}

func (d *Devcontainer) labels() []string {
	return []string{"--filter", "label=com.docker.compose.project=" + d.Compose, "--filter", "label=com.docker.compose.service=" + d.Service}
}

// Running returns the ID of the project's running container, or "".
func (d *Devcontainer) Running(ctx context.Context) (string, error) {
	args := append(append([]string{"ps", "-q"}, d.labels()...), "--filter", "label=devcontainer.local_folder="+d.Dir)
	text, err := docker(ctx, args...)
	if err != nil {
		return "", err
	}
	ids := strings.Fields(text)
	switch len(ids) {
	case 0:
		return "", nil
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%d containers are running for %s (%s), and there should be one → the human can remove the extra ones with docker rm -f", len(ids), d.Project, strings.Join(ids, ", "))
}

func nonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// Claim removes a container left over from a moved checkout, so that it is never reused with an
// empty mount, and refuses when that other checkout is live and running there (F2, R48). A container
// nothing identifies is left alone. It also writes the fragment, since VS Code runs this first.
func (d *Devcontainer) Claim(ctx context.Context, out io.Writer) error {
	if err := d.WriteFragment(); err != nil {
		return err
	}
	text, err := docker(ctx, append([]string{"ps", "-aq"}, d.labels()...)...)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(text) {
		c, err := inspect(ctx, id)
		if err != nil {
			return err
		}
		other := c.folder()
		switch {
		case other == "":
			return fmt.Errorf("container %s has %s's Compose name, but nothing says which checkout it is from → the human can remove it with docker rm -f %s", id, d.Project, id)
		case samePath(other, d.Dir):
			continue
		case c.State.Running && nonEmptyDir(hostPath(other)):
			return fmt.Errorf("the %s container %s is running from another checkout, %s → stop it there, or remove it with docker rm -f %s, so that this checkout can own the container", d.Compose, id, other, id)
		}
		fmt.Fprintf(out, "Removing container %s, left over from %s\n", id, other)
		if _, err := docker(ctx, "rm", "-f", id); err != nil {
			return err
		}
	}
	return nil
}

// Check compares a running container with what the configuration requires. Ports and mounts are
// fixed when a container is created, so a mismatch needs a new container, which is the human's call
// (F3, R50).
func (d *Devcontainer) Check(ctx context.Context, id string) error {
	c, err := inspect(ctx, id)
	if err != nil {
		return err
	}
	fix := "the human can run aw containers recreate " + d.Project
	mounted := false
	for _, m := range c.Mounts {
		if samePath(m.Source, d.Dir) && samePath(m.Destination, d.Dir) {
			mounted = true
		}
	}
	if !mounted {
		return fmt.Errorf("the %s container does not mount %s at the same path, and paths must mean the same inside and outside → %s", d.Project, d.Dir, fix)
	}
	var missing []string
	for _, p := range d.Ports {
		found := false
		for _, b := range c.HostConfig.PortBindings[strconv.Itoa(p)+"/tcp"] {
			found = found || b.HostPort == strconv.Itoa(p)
		}
		if !found {
			missing = append(missing, strconv.Itoa(p))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the %s container does not publish port %s, which a seat may use, and ports are fixed when a container is created → %s", d.Project, strings.Join(missing, ", "), fix)
	}
	return nil
}

// flock takes the project's start lock, so that two commands cannot race to build rival containers.
// No state lock is held while it waits.
func (d *Devcontainer) flock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(d.lock), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(d.lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }()
	select {
	case err := <-done:
		if err != nil {
			f.Close()
			return nil, err
		}
		return func() { f.Close() }, nil
	case <-ctx.Done():
		go func() { <-done; f.Close() }()
		return nil, ctx.Err()
	}
}

// Up makes sure the project's container is running and matches the configuration, and returns its
// ID. It starts one only through devcontainer up (F1), showing the CLI's progress on out.
func (d *Devcontainer) Up(ctx context.Context, out io.Writer) (string, error) {
	id, err := d.Running(ctx)
	if err != nil {
		return "", err
	}
	if id == "" {
		unlock, err := d.flock(ctx)
		if err != nil {
			return "", err
		}
		defer unlock()
		if id, err = d.Running(ctx); err != nil { // someone else may have started it while we waited
			return "", err
		}
	}
	if id == "" {
		if err := d.Claim(ctx, out); err != nil {
			return "", err
		}
		if len(d.cli) == 0 {
			return "", errors.New("[devcontainer] command is empty → set the devcontainer CLI's command")
		}
		c := exec.CommandContext(ctx, d.cli[0], append(d.cli[1:], "up", "--workspace-folder", d.Dir)...)
		c.Dir, c.Stdout, c.Stderr = d.Dir, out, out
		c.Env = append(os.Environ(), d.cliEnv...)
		fmt.Fprintf(out, "Starting the %s container with devcontainer up…\n", d.Project)
		if err := c.Run(); err != nil {
			return "", fmt.Errorf("devcontainer up failed for %s: %w → its output is above", d.Project, err)
		}
		if id, err = d.Running(ctx); err != nil {
			return "", err
		}
		if id == "" {
			return "", fmt.Errorf("devcontainer up finished, but no %s container of Compose project %s is running → check the service name and the Compose name in .devcontainer", d.Service, d.Compose)
		}
	}
	return id, d.Check(ctx, id)
}

// Sessions lists the processes attached to a running container: VS Code, terminals, aw run, servers.
// Each exec session is a child of the container's shim, beside its main process.
func (d *Devcontainer) Sessions(ctx context.Context, id string) ([]string, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	text, err := docker(ctx, "top", id, "-eo", "pid,ppid,args")
	if err != nil {
		return nil, err
	}
	type row struct{ pid, ppid, args string }
	var rows []row
	parent := ""
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 3 {
			continue
		}
		r := row{f[0], f[1], strings.Join(f[2:], " ")}
		rows = append(rows, r)
		if r.pid == strconv.Itoa(c.State.Pid) {
			parent = r.ppid
		}
	}
	var out []string
	for _, r := range rows {
		if parent != "" && r.ppid == parent && r.pid != strconv.Itoa(c.State.Pid) {
			out = append(out, r.args)
		}
	}
	return out, nil
}

// Remove stops the project's containers, without their volumes.
func (d *Devcontainer) Remove(ctx context.Context) error {
	_, err := docker(ctx, "compose", "-p", d.Compose, "down")
	return err
}

// Listening reports whether something in the running container listens on a port. Docker's proxy
// holds the port on the host whether or not anything listens, so only the container can say.
func (d *Devcontainer) Listening(ctx context.Context, port int) bool {
	id, err := d.Running(ctx)
	if err != nil || id == "" {
		return false
	}
	text, err := docker(ctx, "exec", id, "sh", "-c", "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null")
	if err != nil {
		return false
	}
	want := fmt.Sprintf(":%04X", port)
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) > 3 && f[3] == "0A" && strings.HasSuffix(f[1], want) {
			return true
		}
	}
	return false
}

// ExecArgs is docker exec's arguments up to the command. Environment variables are named, and the
// docker client takes their values from its own environment, so no value appears in an argument list.
func (d *Devcontainer) ExecArgs(id, dir string, env []string, interactive string) []string {
	args := []string{"exec", interactive}
	if d.User != "" {
		args = append(args, "-u", d.User)
	}
	args = append(args, "-w", dir)
	for _, name := range EnvNames(env) {
		args = append(args, "-e", name)
	}
	return append(args, id)
}

// EnvNames returns the names of KEY=VALUE pairs, each once.
func EnvNames(env []string) []string {
	var out []string
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// Within returns a directory's real path, which is the path inside the container too, and refuses
// one the container does not see.
func (d *Devcontainer) Within(dir string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if dir != d.Dir && !strings.HasPrefix(dir, d.Dir+"/") {
		return "", fmt.Errorf("%s is outside %s, which is all the %s container mounts → run this from the project's own worktree", dir, d.Dir, d.Project)
	}
	return dir, nil
}

// Interactive is whether docker exec should get a terminal.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
