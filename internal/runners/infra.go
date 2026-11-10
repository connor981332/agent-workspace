package runners

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/workspace"
)

// Infra is a workspace's shared infrastructure: infra/compose.yaml and infra/compose.d/*.yaml.
type Infra struct {
	Owner    string
	Project  string
	Network  string
	Services []string
	PortEnv  []string // AW_PORT_<n>=<n + offset>, which the Compose files read
	Offset   int
	// RehearsalNamespace is set when this is the infrastructure of a throwaway copy of the workspace.
	RehearsalNamespace string
}

// NewInfra describes the shared infrastructure.
func NewInfra(owner, namespace string, services []string, ports []int, offset int) *Infra {
	i := &Infra{Owner: owner, Project: namespace + "-infra", Network: namespace + "-dev-net", Services: services, Offset: offset}
	if workspace.IsRehearsal(owner) {
		i.RehearsalNamespace = namespace
	} else if data, err := os.ReadFile(filepath.Join(owner, "infra", "compose.yaml")); err == nil {
		// A file's own name is its stack's identity: another project name would start a second copy.
		if m := composeName.FindSubmatch(data); m != nil {
			i.Project = string(m[1])
		}
	}
	for _, p := range ports {
		i.PortEnv = append(i.PortEnv, "AW_PORT_"+strconv.Itoa(p)+"="+strconv.Itoa(p+offset))
	}
	return i
}

var composeName = regexp.MustCompile(`(?m)^name:\s*["']?([^"'$\s#]+)["']?\s*(?:#.*)?$`)

var awPortRE = regexp.MustCompile(`AW_PORT_(\d+)`)

// ComposeEnv is what Compose files, and the initializeCommand a devcontainer runs on the host, can
// read: AW_PORT_<n> for each ${AW_PORT_n:-n} the files use, and in a rehearsal the same names lane
// commands get, so that a hand-written file or script can leave the owner's containers alone.
func ComposeEnv(files []string, offset int, rehearsalNamespace string) []string {
	seen := map[string]bool{}
	var env []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range awPortRE.FindAllStringSubmatch(string(data), -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && !seen[m[1]] {
				seen[m[1]] = true
				env = append(env, "AW_PORT_"+m[1]+"="+strconv.Itoa(n+offset))
			}
		}
	}
	if rehearsalNamespace != "" {
		env = append(env, "AW_REHEARSAL=1", "AW_NAMESPACE="+rehearsalNamespace,
			"AW_INFRA_PROJECT="+rehearsalNamespace+"-infra", "AW_INFRA_NETWORK="+rehearsalNamespace+"-dev-net")
	}
	return env
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
	c.Env = append(append(append(os.Environ(), i.PortEnv...), ComposeEnv(files, i.Offset, i.RehearsalNamespace)...), "COMPOSE_FILE="+strings.Join(files, ":"), "COMPOSE_PATH_SEPARATOR=:")
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

// Destroy removes throwaway infrastructure, its volumes and its network.
func (i *Infra) Destroy(ctx context.Context, out io.Writer) error {
	err := i.compose(ctx, out, "down", "-v")
	if _, nerr := docker(ctx, "network", "rm", i.Network); err == nil && nerr != nil {
		err = nerr
	}
	return err
}

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
