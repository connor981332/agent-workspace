// Package providers owns per-lane data (lifecycle.md, "Providers"): where getting teardown wrong
// destroys something. A provider acts only on what is recorded as the lane's, drops by exact recorded
// name, and never connects to a template.
package providers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connor981332/agent-workspace/internal/config"
	"github.com/connor981332/agent-workspace/internal/state"
)

// Lane is what a provider knows about the lane it acts on.
type Lane struct {
	Number int    `json:"number"`
	Dir    string `json:"lane"`
	Name   string `json:"name"`
	Root   string `json:"root"`
	State  string `json:"state"`
}

// Provider is one kind of per-lane data. owned is what is recorded as the lane's. record must be
// called before a new object is created, so that a crash leaves nothing unrecorded.
type Provider interface {
	// Create makes the lane's data, from the template where there is one.
	Create(ctx context.Context, l Lane, owned []string, record func(string) error) error
	// Reset returns the lane's data to its initial state.
	Reset(ctx context.Context, l Lane, owned []string, record func(string) error) error
	// Env returns the variables a lane's commands receive.
	Env(ctx context.Context, l Lane, owned []string) (map[string]string, error)
	// Discover returns objects project code made from recorded ones (S18), leaving out foreign.
	Discover(ctx context.Context, l Lane, owned, foreign []string) ([]string, error)
	// Drop removes one recorded object.
	Drop(ctx context.Context, l Lane, name string) error
	// Snapshot copies the lane's data under a name, and returns the objects it made.
	Snapshot(ctx context.Context, l Lane, name string, owned []string) ([]string, error)
	// Restore replaces the lane's data with a snapshot's objects.
	Restore(ctx context.Context, l Lane, name string, objects, owned []string) error
	// Describe reports what exists, for aw and aw doctor.
	Describe(ctx context.Context, l Lane, owned []string) (string, error)
}

// ErrNoSnapshots says a provider cannot take snapshots.
var ErrNoSnapshots = errors.New("this provider takes no snapshots")

var snapshotName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// CheckSnapshotName refuses a name that could not be part of a database name.
func CheckSnapshotName(name string) error {
	if !snapshotName.MatchString(name) {
		return fmt.Errorf("%q is not a snapshot name → use lowercase letters, digits and underscores, starting with a letter, at most 31 characters", name)
	}
	return nil
}

// Set is a workspace's data, for one lane's commands.
type Set struct {
	Cfg      *config.Config
	Store    *state.Store
	Owner    string // the owner checkout, where external providers run
	StateDir func(lane int) (string, error)
}

// New returns the provider for a data block.
func (s *Set) New(name string) (Provider, error) {
	d, ok := s.Cfg.Lanes.Data[name]
	if !ok {
		return nil, fmt.Errorf("there is no [lanes.data.%s] → check workspace.toml", name)
	}
	switch d.Provider {
	case "postgres":
		return newPostgres(d, s.Cfg.Workspace.Namespace)
	case "redis":
		return newRedis(d)
	case "external":
		return &external{name: name, data: d, owner: s.Owner}, nil
	}
	return nil, fmt.Errorf("[lanes.data.%s] has an unknown provider %q → use postgres, redis or external", name, d.Provider)
}

// Names returns the workspace's data names, sorted.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.Cfg.Lanes.Data))
	for name := range s.Cfg.Lanes.Data {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func (s *Set) recorder(ctx context.Context, lane int, data string) func(string) error {
	return func(object string) error {
		return s.Store.Write(ctx, func(tx *sql.Tx) error { return state.RecordLaneData(tx, lane, data, object) })
	}
}

// Provision creates or resets a lane's data, and records the variables its commands receive.
func (s *Set) Provision(ctx context.Context, l Lane, data string, fromPool bool) error {
	p, err := s.New(data)
	if err != nil {
		return err
	}
	owned, err := state.LaneData(s.Store.DB(), l.Number, data)
	if err != nil {
		return err
	}
	record := s.recorder(ctx, l.Number, data)
	// A recycled lane that was given no data, or a lane whose earlier attempt stopped part-way, has
	// nothing to reset, so it is created. What it did create is recorded and is reused.
	if fromPool && len(owned) > 0 {
		// Copies project code made since the last look (tests, usually) go back to nothing too.
		if err := s.Discover(ctx, l); err != nil {
			return err
		}
		if owned, err = state.LaneData(s.Store.DB(), l.Number, data); err != nil {
			return err
		}
		err = p.Reset(ctx, l, owned, record)
	} else {
		err = p.Create(ctx, l, owned, record)
	}
	if err != nil {
		return err
	}
	return s.WriteEnv(ctx, l, data)
}

// WriteEnv records the variables a lane's commands receive from one data block, including the
// block's env templates.
func (s *Set) WriteEnv(ctx context.Context, l Lane, data string) error {
	p, err := s.New(data)
	if err != nil {
		return err
	}
	owned, err := state.LaneData(s.Store.DB(), l.Number, data)
	if err != nil {
		return err
	}
	vars, err := p.Env(ctx, l, owned)
	if err != nil {
		return err
	}
	out := map[string]string{}
	prefix := "AW_" + envWord(data) + "_"
	for k, v := range vars {
		if !strings.HasPrefix(k, "{") {
			out[prefix+k] = v
		}
	}
	for k, tmpl := range s.Cfg.Lanes.Data[data].Env {
		v, err := render(tmpl, vars, l)
		if err != nil {
			return fmt.Errorf("[lanes.data.%s.env] %s: %w", data, k, err)
		}
		out[k] = v
	}
	dir, err := s.StateDir(l.Number)
	if err != nil {
		return err
	}
	return writeEnvFile(filepath.Join(dir, "data", data+".env"), out)
}

var placeholder = regexp.MustCompile(`\{[a-z]+(:[A-Za-z0-9_]+)?\}`)

// render fills a value's placeholders: {lane}, {name}, {url} and {url:<database>}. A provider's Env
// offers them under keys of the form "{url:x}".
func render(tmpl string, vars map[string]string, l Lane) (string, error) {
	var bad error
	out := placeholder.ReplaceAllStringFunc(tmpl, func(p string) string {
		switch p {
		case "{lane}":
			return l.Dir
		case "{name}":
			return l.Name
		}
		if v, ok := vars[p]; ok {
			return v
		}
		bad = fmt.Errorf("%s is not something this provider offers", p)
		return p
	})
	return out, bad
}

func envWord(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, s)
}

func writeEnvFile(path string, vars map[string]string) error {
	keys := make([]string, 0, len(vars))
	for k, v := range vars {
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("the value of %s has a line break, which an env file cannot hold", k)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, vars[k])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Discover records objects project code made from the lane's recorded ones (S18). Another lane's
// recorded names are never taken, however they look.
func (s *Set) Discover(ctx context.Context, l Lane) error {
	for _, data := range s.Names() {
		p, err := s.New(data)
		if err != nil {
			return err
		}
		owned, err := state.LaneData(s.Store.DB(), l.Number, data)
		if err != nil {
			return err
		}
		if len(owned) == 0 {
			continue
		}
		foreign, err := state.OtherLanesData(s.Store.DB(), l.Number, data)
		if err != nil {
			return err
		}
		found, err := p.Discover(ctx, l, owned, foreign)
		if err != nil {
			return fmt.Errorf("listing %s's data: %w", data, err)
		}
		record := s.recorder(ctx, l.Number, data)
		for _, name := range found {
			if err := record(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// DropLane removes all of a lane's recorded data, by exact name. Each object's record goes when the
// object does, so a retry finishes the job and never touches anything else.
func (s *Set) DropLane(ctx context.Context, l Lane) error {
	if err := s.Discover(ctx, l); err != nil {
		return err
	}
	for _, data := range s.Names() {
		p, err := s.New(data)
		if err != nil {
			return err
		}
		owned, err := state.LaneData(s.Store.DB(), l.Number, data)
		if err != nil {
			return err
		}
		// Longest first: a copy goes before the database it was named after.
		slices.SortFunc(owned, func(a, b string) int { return len(b) - len(a) })
		for _, object := range owned {
			if err := p.Drop(ctx, l, object); err != nil {
				return fmt.Errorf("dropping %s of %s: %w", object, data, err)
			}
			if err := s.Store.Write(ctx, func(tx *sql.Tx) error { return state.ForgetLaneData(tx, l.Number, data, object) }); err != nil {
				return err
			}
		}
	}
	return nil
}

// Snapshot takes a named snapshot of the lane's data. Names are shared across the workspace, and a
// name in use is never overwritten.
func (s *Set) Snapshot(ctx context.Context, l Lane, name string) ([]string, error) {
	if err := CheckSnapshotName(name); err != nil {
		return nil, err
	}
	var taken []string
	for _, data := range s.Names() {
		if _, err := state.SnapshotByName(s.Store.DB(), data, name); err == nil {
			return nil, fmt.Errorf("a snapshot named %s already exists, and a name in use is never overwritten → choose another name", name)
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
	}
	for _, data := range s.Names() {
		p, err := s.New(data)
		if err != nil {
			return nil, err
		}
		owned, err := state.LaneData(s.Store.DB(), l.Number, data)
		if err != nil {
			return nil, err
		}
		objects, err := p.Snapshot(ctx, l, name, owned)
		if errors.Is(err, ErrNoSnapshots) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshotting %s: %w", data, err)
		}
		if err := s.Store.Write(ctx, func(tx *sql.Tx) error {
			return state.RecordSnapshot(tx, state.Snapshot{Data: data, Name: name, Lane: l.Number, Objects: objects})
		}); err != nil {
			return nil, err
		}
		taken = append(taken, data)
	}
	if len(taken) == 0 {
		return nil, errors.New("nothing in this workspace takes snapshots → only the postgres provider and external providers do")
	}
	return taken, nil
}

// Restore replaces the lane's data with a named snapshot.
func (s *Set) Restore(ctx context.Context, l Lane, name string) ([]string, error) {
	var restored []string
	for _, data := range s.Names() {
		snap, err := state.SnapshotByName(s.Store.DB(), data, name)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p, err := s.New(data)
		if err != nil {
			return nil, err
		}
		owned, err := state.LaneData(s.Store.DB(), l.Number, data)
		if err != nil {
			return nil, err
		}
		if err := p.Restore(ctx, l, name, snap.Objects, owned); err != nil {
			return nil, fmt.Errorf("restoring %s: %w", data, err)
		}
		restored = append(restored, data)
	}
	if len(restored) == 0 {
		return nil, fmt.Errorf("there is no snapshot named %s → aw snapshots lists them", name)
	}
	return restored, nil
}
