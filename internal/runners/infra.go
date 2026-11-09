package runners

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Infra is a workspace's shared infrastructure: infra/compose.yaml and infra/compose.d/*.yaml.
type Infra struct {
	Owner    string
	Project  string
	Network  string
	Services []string
	PortEnv  []string // AW_PORT_<n>=<n + offset>, which the Compose files read
}

// NewInfra describes the shared infrastructure.
func NewInfra(owner, namespace string, services []string, ports []int, offset int) *Infra {
	i := &Infra{Owner: owner, Project: namespace + "-infra", Network: namespace + "-dev-net", Services: services}
	for _, p := range ports {
		i.PortEnv = append(i.PortEnv, "AW_PORT_"+strconv.Itoa(p)+"="+strconv.Itoa(p+offset))
	}
	return i
}

// Configured reports whether the workspace has any shared infrastructure.
func (i *Infra) Configured() bool {
	_, err := os.Stat(filepath.Join(i.Owner, "infra", "compose.yaml"))
	return err == nil
}

func (i *Infra) compose(ctx context.Context, out io.Writer, args ...string) error {
	files := []string{filepath.Join(i.Owner, "infra", "compose.yaml")}
	extra, _ := filepath.Glob(filepath.Join(i.Owner, "infra", "compose.d", "*.yaml"))
	slices.Sort(extra)
	files = append(files, extra...)
	c := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", i.Project}, args...)...)
	c.Dir, c.Stdout, c.Stderr = i.Owner, out, out
	c.Env = append(append(os.Environ(), i.PortEnv...), "COMPOSE_FILE="+strings.Join(files, ":"), "COMPOSE_PATH_SEPARATOR=:")
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker compose %s failed: %w → its output is above", args[0], err)
	}
	return nil
}

// Running lists the services running, by name, so that a leftover container of a removed service
// cannot stand in for a missing one (F7).
func (i *Infra) Running(ctx context.Context) ([]string, error) {
	text, err := docker(ctx, "ps", "--filter", "label=com.docker.compose.project="+i.Project, "--filter", "status=running", "--format", `{{.Label "com.docker.compose.service"}}`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(text), nil
}

// Up starts whatever is not running. When every service is, it is one docker ps (F7). It creates the
// shared network first, whichever project starts first.
func (i *Infra) Up(ctx context.Context, out io.Writer) error {
	if !i.Configured() {
		return nil
	}
	running, err := i.Running(ctx)
	if err != nil {
		return err
	}
	need := len(i.Services) == 0
	for _, s := range i.Services {
		need = need || !slices.Contains(running, s)
	}
	if !need {
		return nil
	}
	if _, err := docker(ctx, "network", "inspect", i.Network); err != nil {
		if _, err := docker(ctx, "network", "create", i.Network); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Starting the shared infrastructure (%s)…\n", i.Project)
	return i.compose(ctx, out, "up", "-d", "--remove-orphans")
}

// Down stops the shared infrastructure. Its volumes are kept.
func (i *Infra) Down(ctx context.Context, out io.Writer) error { return i.compose(ctx, out, "down") }

// Status shows the shared infrastructure's containers.
func (i *Infra) Status(ctx context.Context, out io.Writer) error { return i.compose(ctx, out, "ps") }

// Logs shows the shared infrastructure's logs.
func (i *Infra) Logs(ctx context.Context, out io.Writer, args []string) error {
	return i.compose(ctx, out, append([]string{"logs"}, args...)...)
}

// BaseCompose is infra/compose.yaml, which the modules' fragments extend.
func BaseCompose(project, network string) string {
	return fmt.Sprintf("# Written by aw add module. Service definitions are in infra/compose.d/.\nname: %s\n\nservices: {}\n\nnetworks:\n  default:\n    name: %s\n    external: true\n", project, network)
}
