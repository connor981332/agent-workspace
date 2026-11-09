package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
)

// redis is the built-in provider: a database number per lane, which is the lane's number. The owner
// uses database 0. It keeps no snapshots: Redis has no cheap copy.
type redis struct {
	data *config.Data
	u    *url.URL
}

func newRedis(d *config.Data) (*redis, error) {
	u, err := url.Parse(d.URL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("the redis url %q is not usable → write redis://host:6379", redactURL(d.URL))
	}
	return &redis{data: d, u: u}, nil
}

func (r *redis) flush(ctx context.Context, number int) error {
	args := []string{"-n", strconv.Itoa(number), "FLUSHDB"}
	password, hasPassword := "", false
	if r.u.User != nil {
		password, hasPassword = r.u.User.Password()
	}
	env := os.Environ()
	if hasPassword {
		env = append(env, "REDISCLI_AUTH="+password)
	}
	var cmd *exec.Cmd
	if r.data.Service != "" {
		container, err := findContainer(ctx, r.data.Service)
		if err != nil {
			return err
		}
		execArgs := []string{"exec"}
		if hasPassword || os.Getenv("REDISCLI_AUTH") != "" {
			execArgs = append(execArgs, "-e", "REDISCLI_AUTH")
		}
		cmd = exec.CommandContext(ctx, "docker", append(append(execArgs, container, "redis-cli"), args...)...)
	} else {
		host := []string{"-h", r.u.Hostname()}
		if p := r.u.Port(); p != "" {
			host = append(host, "-p", p)
		}
		cmd = exec.CommandContext(ctx, "redis-cli", append(host, args...)...)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("redis-cli: %v: %s → check that the server is running and [lanes.data] url", err, strings.TrimSpace(stderr.String()))
	}
	if out := strings.TrimSpace(stdout.String()); out != "OK" {
		return fmt.Errorf("redis-cli: %s → check that the server is running and [lanes.data] url", out)
	}
	return nil
}

func object(l Lane) string { return "db " + strconv.Itoa(l.Number) }

func (r *redis) Create(ctx context.Context, l Lane, owned []string, record func(string) error) error {
	if len(owned) == 0 {
		if err := record(object(l)); err != nil {
			return err
		}
	}
	return r.flush(ctx, l.Number)
}

func (r *redis) Reset(ctx context.Context, l Lane, _ []string, _ func(string) error) error {
	return r.flush(ctx, l.Number)
}

func (r *redis) Env(_ context.Context, l Lane, _ []string) (map[string]string, error) {
	u := *r.u
	u.Path = "/" + strconv.Itoa(l.Number)
	return map[string]string{"URL": u.String(), "{url}": u.String()}, nil
}

func (r *redis) Discover(context.Context, Lane, []string, []string) ([]string, error) {
	return nil, nil
}

// Drop flushes the lane's database, the only object it ever records.
func (r *redis) Drop(ctx context.Context, l Lane, name string) error {
	if name != object(l) {
		return fmt.Errorf("%s is not lane %s's Redis database, so aw will not flush it", name, l.Dir)
	}
	return r.flush(ctx, l.Number)
}

func (r *redis) Snapshot(context.Context, Lane, string, []string) ([]string, error) {
	return nil, ErrNoSnapshots
}

func (r *redis) Restore(context.Context, Lane, string, []string, []string) error {
	return ErrNoSnapshots
}

func (r *redis) Describe(_ context.Context, l Lane, _ []string) (string, error) {
	return "redis database " + strconv.Itoa(l.Number), nil
}

// findContainer resolves a service: a Compose service with exactly one running container, or a
// container of that name. Anything ambiguous is refused, so that aw never picks another project's.
func findContainer(ctx context.Context, service string) (string, error) {
	list := func(filter string) ([]string, error) {
		out, err := exec.CommandContext(ctx, "docker", "ps", "-q", "--filter", filter, "--filter", "status=running").Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return nil, fmt.Errorf("docker ps: %s → check that Docker is running", strings.TrimSpace(string(ee.Stderr)))
			}
			return nil, fmt.Errorf("docker ps: %w → check that Docker is installed", err)
		}
		return strings.Fields(string(out)), nil
	}
	ids, err := list("label=com.docker.compose.service=" + service)
	if err != nil {
		return "", err
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("%d running containers are Compose service %s → set service to one exact container name", len(ids), service)
	}
	if len(ids) == 0 {
		if ids, err = list("name=^/" + service + "$"); err != nil {
			return "", err
		}
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("no running container is service %s → start it, or check [lanes.data] service", service)
	}
	return ids[0], nil
}
