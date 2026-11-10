package providers

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/workspace"
)

// maxIdentifier is the longest name Postgres keeps: a longer one is silently cut, and would then
// name something else.
const maxIdentifier = 63

// postgres is the built-in provider: lane databases <name>_<lane>, cloned with CREATE DATABASE …
// TEMPLATE. It shells out to psql, on the host or in a configured service, so no driver is linked.
// Statements go to the maintenance database, so nothing connects to the template or a lane database.
type postgres struct {
	onlyProject string // a rehearsal's own infrastructure, or ""
	owner       string // the owner checkout, where build_template runs
	data        *config.Data
	namespace   string
	u           *url.URL
}

func newPostgres(d *config.Data, namespace string) (*postgres, error) {
	u, err := url.Parse(d.URL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("the postgres url %q is not usable → write postgres://user@host:5432", redactURL(d.URL))
	}
	return &postgres{data: d, namespace: namespace, u: u}, nil
}

// redactURL hides a password, for messages.
func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			return u.String()
		}
	}
	return s
}

func quote(id string) string { return `"` + id + `"` }

func (p *postgres) laneDB(base string, l Lane) string { return base + "_" + l.Dir }

func checkLength(name string) error {
	if len(name) > maxIdentifier {
		return fmt.Errorf("%s is longer than Postgres's limit of %d characters, so it would be cut to another name → use shorter names", name, maxIdentifier)
	}
	return nil
}

// psql runs one statement against the maintenance database. Connection settings go through the
// environment, so that a password never appears in a process list.
// A new Postgres container starts a temporary server, shuts it down, and starts the real one, so a
// volume that is still empty refuses connections for a few seconds even after the container is up.
const postgresStartWait = 30 * time.Second

func postgresStarting(message string) bool {
	return strings.Contains(message, "the database system is") ||
		(strings.Contains(message, "/.s.PGSQL.") && strings.Contains(message, "No such file or directory"))
}

func (p *postgres) psql(ctx context.Context, statement string) (string, error) {
	deadline := time.Now().Add(postgresStartWait)
	for {
		out, err := p.psqlOnce(ctx, statement)
		if err == nil || !postgresStarting(err.Error()) || time.Now().After(deadline) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (p *postgres) psqlOnce(ctx context.Context, statement string) (string, error) {
	user := "postgres"
	if p.u.User != nil && p.u.User.Username() != "" {
		user = p.u.User.Username()
	}
	password, hasPassword := "", false
	if p.u.User != nil {
		password, hasPassword = p.u.User.Password()
	}
	args := []string{"-v", "ON_ERROR_STOP=1", "-qAt", "-c", statement}
	var cmd *exec.Cmd
	env := os.Environ()
	if hasPassword {
		env = append(env, "PGPASSWORD="+password)
	}
	if p.data.Service != "" {
		container, err := findContainer(ctx, p.data.Service, p.onlyProject)
		if err != nil {
			return "", err
		}
		execArgs := []string{"exec", "-i"}
		if hasPassword || os.Getenv("PGPASSWORD") != "" {
			execArgs = append(execArgs, "-e", "PGPASSWORD")
		}
		execArgs = append(execArgs, container, "psql", "-U", user, "-d", "postgres")
		cmd = exec.CommandContext(ctx, "docker", append(execArgs, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, "psql", args...)
		env = append(env, "PGHOST="+p.u.Hostname(), "PGUSER="+user, "PGDATABASE=postgres")
		if port := p.u.Port(); port != "" {
			env = append(env, "PGPORT="+port)
		}
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if len(stderr.Bytes()) == 0 {
			return "", fmt.Errorf("psql: %w → check that the server is running and [lanes.data] url", err)
		}
		return "", fmt.Errorf("psql: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ensureTemplate runs build_template if the template does not exist. Lanes start together, so a lock
// across processes makes it one build: the others wait, and then find the template there.
func (p *postgres) ensureTemplate(ctx context.Context) error {
	if p.data.Template == "" || p.data.BuildTemplate == "" {
		return nil
	}
	if ok, err := p.exists(ctx, p.data.Template); err != nil || ok {
		return err
	}
	dir, err := (&workspace.Workspace{Owner: p.owner}).StateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "template-"+p.data.Template+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if ok, err := p.exists(ctx, p.data.Template); err != nil || ok {
		return err // another lane built it while this one waited
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", p.data.BuildTemplate)
	cmd.Dir = p.owner
	cmd.Env = append(os.Environ(), "AW_TEMPLATE="+p.data.Template, "AW_PHASE=data", "AW_OWNER="+p.owner)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if len(text) > 2000 {
			text = "…" + text[len(text)-2000:]
		}
		return fmt.Errorf("build_template failed for %s (%v): %s → fix the command in [lanes.data]", p.data.Template, err, text)
	}
	if ok, err := p.exists(ctx, p.data.Template); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("build_template finished, but there is still no database %s → make the command create it (AW_TEMPLATE names it)", p.data.Template)
	}
	return nil
}

func (p *postgres) exists(ctx context.Context, name string) (bool, error) {
	out, err := p.psql(ctx, fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = '%s'`, name))
	return out != "", err
}

func (p *postgres) create(ctx context.Context, name, from string) error {
	statement := `CREATE DATABASE ` + quote(name)
	if from != "" {
		statement += ` TEMPLATE ` + quote(from)
	}
	_, err := p.psql(ctx, statement)
	return err
}

func (p *postgres) terminate(ctx context.Context, name string) error {
	if !p.data.Terminate {
		return nil
	}
	_, err := p.psql(ctx, fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, name))
	return err
}

func (p *postgres) drop(ctx context.Context, name string) error {
	if name == p.data.Template {
		return fmt.Errorf("%s is the template, which is never dropped", name)
	}
	if err := p.terminate(ctx, name); err != nil {
		return err
	}
	if _, err := p.psql(ctx, `DROP DATABASE IF EXISTS `+quote(name)); err != nil {
		return inUse(err)
	}
	return nil
}

func inUse(err error) error {
	if strings.Contains(err.Error(), "being accessed by other users") {
		return fmt.Errorf("%w → stop what is connected to it, or set terminate = true on the data block", err)
	}
	return err
}

// Create makes each lane database. One that is already recorded came from an earlier attempt, and is
// kept. One that exists but is not recorded belongs to someone else, and is refused.
func (p *postgres) Create(ctx context.Context, l Lane, owned []string, record func(string) error) error {
	if err := p.ensureTemplate(ctx); err != nil {
		return err
	}
	for _, base := range p.data.Databases {
		name := p.laneDB(base, l)
		if err := checkLength(name); err != nil {
			return err
		}
		ok, err := p.exists(ctx, name)
		if err != nil {
			return err
		}
		if slices.Contains(owned, name) {
			if ok {
				continue
			}
		} else {
			if ok {
				return fmt.Errorf("the database %s exists, and is not recorded as this lane's, so aw will not touch it → rename or drop it yourself, or change [lanes.data] databases", name)
			}
			if err := record(name); err != nil {
				return err
			}
		}
		if err := p.create(ctx, name, p.data.Template); err != nil {
			return inUse(err)
		}
	}
	return nil
}

// Reset drops the lane's recorded databases and creates them again from the template.
func (p *postgres) Reset(ctx context.Context, l Lane, owned []string, record func(string) error) error {
	for _, name := range owned {
		base := false
		for _, b := range p.data.Databases {
			base = base || p.laneDB(b, l) == name
		}
		if base {
			continue
		}
		if err := p.drop(ctx, name); err != nil { // a copy project code made
			return err
		}
	}
	for _, b := range p.data.Databases {
		name := p.laneDB(b, l)
		if !slices.Contains(owned, name) {
			if ok, err := p.exists(ctx, name); err != nil {
				return err
			} else if ok {
				return fmt.Errorf("the database %s exists, and is not recorded as this lane's, so aw will not touch it → rename or drop it yourself", name)
			}
			if err := record(name); err != nil {
				return err
			}
		} else if err := p.drop(ctx, name); err != nil {
			return err
		}
		if err := p.create(ctx, name, p.data.Template); err != nil {
			return inUse(err)
		}
	}
	return nil
}

func (p *postgres) urlOf(name string) string {
	u := *p.u
	u.Path = "/" + name
	return u.String()
}

// Env offers AW_<DATA>_URL (the first database), one URL per database, and {url:<database>}.
func (p *postgres) Env(_ context.Context, l Lane, _ []string) (map[string]string, error) {
	vars := map[string]string{}
	for i, base := range p.data.Databases {
		u := p.urlOf(p.laneDB(base, l))
		if i == 0 {
			vars["URL"] = u
			vars["{url}"] = u
		}
		vars["URL_"+envWord(base)] = u
		vars["{url:"+base+"}"] = u
	}
	return vars, nil
}

// Discover lists databases named after a recorded one, followed by an underscore (S18).
func (p *postgres) Discover(ctx context.Context, _ Lane, owned, foreign []string) ([]string, error) {
	var found []string
	for _, name := range owned {
		out, err := p.psql(ctx, fmt.Sprintf(`SELECT datname FROM pg_database WHERE starts_with(datname, '%s_') ORDER BY 1`, name))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Fields(out) {
			if line != p.data.Template && !slices.Contains(owned, line) && !slices.Contains(foreign, line) && !p.isSnapshot(line) {
				found = append(found, line)
			}
		}
	}
	return found, nil
}

func (p *postgres) isSnapshot(name string) bool {
	return strings.HasPrefix(name, p.namespace+"_snapshot_")
}

func (p *postgres) Drop(ctx context.Context, _ Lane, name string) error { return p.drop(ctx, name) }

func (p *postgres) snapshotDB(name, base string) string {
	return p.namespace + "_snapshot_" + name + "_" + base
}

// Snapshot copies the lane's configured databases, not the copies project code made of them.
func (p *postgres) Snapshot(ctx context.Context, l Lane, name string, owned []string) ([]string, error) {
	var made []string
	for _, base := range p.data.Databases {
		src, dst := p.laneDB(base, l), p.snapshotDB(name, base)
		if !slices.Contains(owned, src) {
			return nil, fmt.Errorf("%s is not recorded as this lane's → run aw setup %s", src, l.Name)
		}
		if err := checkLength(dst); err != nil {
			return nil, err
		}
		if err := p.create(ctx, dst, src); err != nil {
			return nil, inUse(err)
		}
		made = append(made, dst)
	}
	return made, nil
}

// Restore makes the new copy before dropping the old one, so that a failed copy loses nothing.
func (p *postgres) Restore(ctx context.Context, l Lane, name string, objects, owned []string) error {
	for _, base := range p.data.Databases {
		src, dst := p.snapshotDB(name, base), p.laneDB(base, l)
		if !slices.Contains(objects, src) {
			return fmt.Errorf("the snapshot %s has no copy of %s → it was taken with different databases", name, base)
		}
		if !slices.Contains(owned, dst) {
			return fmt.Errorf("%s is not recorded as this lane's → run aw setup %s", dst, l.Name)
		}
		incoming := dst + "__restoring"
		if err := checkLength(incoming); err != nil {
			return err
		}
		if err := p.drop(ctx, incoming); err != nil {
			return err
		}
		if err := p.create(ctx, incoming, src); err != nil {
			return inUse(err)
		}
		if err := p.drop(ctx, dst); err != nil {
			p.drop(ctx, incoming)
			return err
		}
		if _, err := p.psql(ctx, `ALTER DATABASE `+quote(incoming)+` RENAME TO `+quote(dst)); err != nil {
			return err
		}
	}
	return nil
}

func (p *postgres) Describe(_ context.Context, _ Lane, owned []string) (string, error) {
	return "databases: " + strings.Join(owned, ", "), nil
}
