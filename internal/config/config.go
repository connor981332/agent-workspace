// Package config reads the three configuration layers (R6): framework defaults, the team layer
// (workspace.toml) and the individual layer (~/.config/agent-workspace/<namespace>.toml). It
// enforces team locks (R7), records where every key came from (R8), decodes strictly with errors
// that name the file, the line and the fix (R9), and compiles the result to a cache.
//
// This package is on the hot path (plans/release-candidate.md §4.3): it may not import os/exec or
// net, directly or transitively.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Config is the merged, validated configuration.
type Config struct {
	Workspace Workspace            `json:"workspace"`
	Lanes     Lanes                `json:"lanes"`
	Lifecycle WorkspaceLifecycle   `json:"lifecycle"`
	Projects  map[string]*Project  `json:"projects"`
	Resources map[string]*Resource `json:"resources"`
	Commands  map[string]*Command  `json:"commands"`
	Guards    map[string]*Guard    `json:"guards"`
	Locks     Locks                `json:"locks"`
	User      User                 `json:"user"`
	Ports     Ports                `json:"ports"`

	Devcontainer Devcontainer     `json:"devcontainer"`
	Roles        map[string]*Role `json:"roles"`
	Publication  Publication      `json:"publication"`
	Delegation   Delegation       `json:"delegation"`
	// LaneEnv are variables lanes receive, by the module or team that adds them.
	LaneEnv  map[string]map[string]string `json:"lane_env"`
	Webhooks map[string]*Webhook          `json:"webhooks"`
	Plans    Plans                        `json:"plans"`
	// Harnesses are declarative adapters: another hooked harness, added without a release.
	Harnesses map[string]*Harness `json:"harnesses"`

	// Values is the merged configuration as decoded TOML values, for aw config get.
	Values map[string]any `json:"-"`
	// Provenance of every leaf key, by Path.key().
	Provenance map[string]*Provenance `json:"-"`
	// Refused lists the individual-layer settings a team lock refused (R7).
	Refused []Refusal `json:"-"`
	// Sources are the files read, for the cache.
	Sources []Source `json:"-"`
}

type Workspace struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Schema    int    `json:"schema"`
	Forge     string `json:"forge"`
}

type Lanes struct {
	Max            int              `json:"max"`
	Pool           int              `json:"pool"`
	Retention      Duration         `json:"retention"`
	Hostname       string           `json:"hostname"`
	ServerCapacity int              `json:"server_capacity"`
	PortStride     int              `json:"port_stride"`
	BranchPrefix   string           `json:"branch_prefix"`
	Data           map[string]*Data `json:"data"`
}

type Data struct {
	Provider  string            `json:"provider"`
	Command   string            `json:"command"`
	Template  string            `json:"template"`
	Databases []string          `json:"databases"`
	URL       string            `json:"url"`
	Terminate bool              `json:"terminate"`
	Service   string            `json:"service"`
	Env       map[string]string `json:"env"`
}

// Role is a team role's permissions (R65).
type Role struct {
	Allow        []string `json:"allow"`
	Deny         []string `json:"deny"`
	Instructions string   `json:"instructions"`
}

// RoleNamed returns a role by name. The implementer role is built in, and a team may redefine it.
func (c *Config) RoleNamed(name string) (*Role, bool) {
	if r, ok := c.Roles[name]; ok {
		return r, true
	}
	if name == "implementer" {
		return &Role{Deny: []string{"publish", "merge"}}, true
	}
	return nil, false
}

// Harness maps a harness's hook payload to the common form. A tool it does not list is refused.
type Harness struct {
	Session    string            `json:"session"`
	Cwd        string            `json:"cwd"`
	Event      string            `json:"event"`
	Tool       string            `json:"tool"`
	Input      string            `json:"input"`
	Command    string            `json:"command"`
	Path       string            `json:"path"`
	Events     map[string]string `json:"events"`
	ShellTools []string          `json:"shell_tools"`
	ReadTools  []string          `json:"read_tools"`
	WriteTools []string          `json:"write_tools"`
	OtherTools []string          `json:"other_tools"`
}

// Webhook is how one provider's sandbox events reach a lane.
type Webhook struct {
	Listen string `json:"listen"`
	Server string `json:"server"`
	Path   string `json:"path"`
}

// Plans is the plan mechanism's setting.
type Plans struct {
	Enabled bool `json:"enabled"`
}

// Publication is what a team has pre-authorized (R72).
type Publication struct {
	Allow []string `json:"allow"`
}

// Delegation is what the human's maintenance delegation covers beyond aw's own commands.
type Delegation struct {
	Maintenance []string `json:"maintenance"`
}

// Devcontainer is how aw runs the devcontainer CLI (S16).
type Devcontainer struct {
	Command string            `json:"command"`
	Version string            `json:"version"`
	Env     map[string]string `json:"env"`
}

type WorkspaceLifecycle struct {
	Prepare  *Hook `json:"prepare"`
	Teardown *Hook `json:"teardown"`
}

type Project struct {
	Directory    string                `json:"directory"`
	Repo         string                `json:"repo"`
	Lanes        string                `json:"lanes"`
	Runner       string                `json:"runner"`
	Install      string                `json:"install"`
	InstallStamp []string              `json:"install_stamp"`
	Run          string                `json:"run"`
	Needs        []string              `json:"needs"`
	DependsOn    []string              `json:"depends_on"`
	Smoke        string                `json:"smoke"`
	Publication  []string              `json:"publication"`
	Contracts    []string              `json:"generated_contracts"`
	Secrets      []string              `json:"secrets"`
	Generated    []string              `json:"generated"`
	LaneGuards   map[string]*LaneGuard `json:"lane_guards"`
	Lifecycle    ProjectLifecycle      `json:"lifecycle"`
	Servers      map[string]*Server    `json:"servers"`
}

// LaneGuard is a check a branch must pass before a lane is created on it.
type LaneGuard struct {
	File    string `json:"file"`
	Matches string `json:"matches"`
	Message string `json:"message"`
}

type ProjectLifecycle struct {
	Prepare *Hook `json:"prepare"`
	Setup   *Hook `json:"setup"`
	Reset   *Hook `json:"reset"`
	Restore *Hook `json:"restore"`
}

type Server struct {
	Command   string   `json:"command"`
	Port      int      `json:"port"`
	OwnerPort int      `json:"owner_port"`
	After     []string `json:"after"`
	Ready     string   `json:"ready"`
}

type Resource struct {
	Kind          string   `json:"kind"`
	Capacity      int      `json:"capacity"`
	Servers       []string `json:"servers"`
	Tokens        []string `json:"tokens"`
	Acquire       string   `json:"acquire"`
	Release       string   `json:"release"`
	OwnerCounts   bool     `json:"owner_counts"`
	OwnerPreempts bool     `json:"owner_preempts"`
	EvictGrace    Duration `json:"evict_grace"`
	MaxHold       Duration `json:"max_hold"`
	IdleExpiry    Duration `json:"idle_expiry"`
	Memory        string   `json:"memory"`
}

type Command struct {
	Project string   `json:"project"`
	Run     string   `json:"run"`
	Needs   []string `json:"needs"`
	After   []string `json:"after"`
}

// Guard is a team guard (R54, S9): declarative, and decided in-process on every tool call.
type Guard struct {
	Commands []string `json:"commands"`
	Paths    []string `json:"paths"`
	Action   string   `json:"action"`
	Message  string   `json:"message"`
	Requires []string `json:"requires"`
}

type Locks struct {
	Keys []string `json:"keys"`
}

type User struct {
	Harness       string      `json:"harness"`
	Multiplexer   Multiplexer `json:"multiplexer"`
	AutoSweep     bool        `json:"auto_sweep"`
	OwnerSessions string      `json:"owner_sessions"`
}

// Multiplexer is where --mux opens a session: a built-in by Name, or a Command template.
type Multiplexer struct {
	Name    string `json:"name,omitempty"`
	Command string `json:"command,omitempty"`
}

func (m *Multiplexer) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*m = Multiplexer{Name: s}
		return nil
	}
	type plain Multiplexer
	return json.Unmarshal(data, (*plain)(m))
}

type Ports struct {
	Offset int `json:"offset"`
}

var databaseRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// redisDatabases is how many databases a default Redis server has.
const redisDatabases = 16

// Hook is a lifecycle step: a command string, or { run, timeout, needs }.
type Hook struct {
	Run     string   `json:"run"`
	Timeout Duration `json:"timeout"`
	Needs   []string `json:"needs"`
}

func (h *Hook) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*h = Hook{Run: s}
		return nil
	}
	type plain Hook
	return json.Unmarshal(data, (*plain)(h))
}

// Duration is a configuration duration ("15s", "14d").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := ParseDuration(s)
	*d = Duration(v)
	return err
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Setting is one layer's value for a key.
type Setting struct {
	Layer  string `json:"layer"`
	Pos    Pos    `json:"position"`
	Value  any    `json:"value"`
	Status string `json:"status"` // effective, overridden, or refused
	Lock   string `json:"lock,omitempty"`
}

// Provenance is the effective value of a key and every layer that set it (R8).
type Provenance struct {
	Key      string    `json:"key"`
	Value    any       `json:"value"`
	Settings []Setting `json:"settings"`
}

// Refusal is an individual-layer setting refused by a team lock.
type Refusal struct {
	Key  string `json:"key"`
	Pos  Pos    `json:"position"`
	Lock string `json:"lock"`
}

func (r Refusal) String() string {
	return fmt.Sprintf("%s: %s is locked by the team (lock %q), so this setting is ignored → remove it, or ask the team to unlock it", r.Pos, r.Key, r.Lock)
}

// Source is a file the configuration was read from, with what identifies its version.
type Source struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Inode   uint64 `json:"inode"`
}

// TeamFile is the team layer's file, relative to the owner checkout.
const TeamFile = "workspace.toml"

// IndividualFile returns the individual layer's path for a namespace.
func IndividualFile(namespace string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "agent-workspace", namespace+".toml")
}

// RegistryFile lists the workspaces on this machine, one owner checkout per line (S15).
func RegistryFile() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "agent-workspace", "workspaces")
}

// PersonalInstructions returns the individual layer's instructions file for a namespace, next to
// its configuration.
func PersonalInstructions(namespace string) string {
	return strings.TrimSuffix(IndividualFile(namespace), ".toml") + ".md"
}

// Load reads and validates the configuration of the workspace whose owner checkout is owner.
// A configuration error is returned as Errors.
func Load(owner string) (*Config, error) {
	teamPath := filepath.Join(owner, TeamFile)
	data, err := os.ReadFile(teamPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Errors{{Pos: Pos{File: teamPath}, Msg: "not found", Fix: "run aw init to create the workspace"}}
		}
		return nil, Errors{{Pos: Pos{File: teamPath}, Msg: "cannot be read: " + err.Error(), Fix: "check the file's permissions"}}
	}
	return load(teamPath, data, func(namespace string) (string, []byte, error) {
		path := IndividualFile(namespace)
		data, err := os.ReadFile(path)
		return path, data, err
	})
}

// LoadBytes validates a team file's contents with no individual layer, for aw config validate on
// an example.
func LoadBytes(teamPath string, data []byte) (*Config, error) {
	return load(teamPath, data, func(string) (string, []byte, error) { return "", nil, os.ErrNotExist })
}

func load(teamPath string, teamData []byte, individual func(namespace string) (string, []byte, error)) (*Config, error) {
	team, err := parseTOML(teamPath, teamData)
	if err != nil {
		return nil, Errors{err.(*Error)}
	}
	errs := validateLayer(team, LayerTeam)
	sources := []Source{sourceOf(teamPath)}

	var indiv *doc
	if ns, ok := nested(team.values, "workspace", "namespace").(string); ok && namespaceRE.MatchString(ns) {
		path, data, err := individual(ns)
		switch {
		case err == nil:
			sources = append(sources, sourceOf(path))
			d, perr := parseTOML(path, data)
			if perr != nil {
				errs = append(errs, perr.(*Error))
			} else {
				indiv = d
				errs = append(errs, validateLayer(d, LayerIndividual)...)
			}
		case errors.Is(err, os.ErrNotExist):
			if path != "" {
				sources = append(sources, Source{Path: path})
			}
		default:
			errs = append(errs, &Error{Pos: Pos{File: path}, Msg: "cannot be read: " + err.Error(), Fix: "check the file's permissions"})
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}

	c := &Config{Values: map[string]any{}, Provenance: map[string]*Provenance{}, Sources: sources}
	locks, lockErrs := parseLocks(team)
	if len(lockErrs) > 0 {
		return nil, lockErrs.sorted()
	}
	c.merge(nil, defaults(), LayerDefault, nil, nil)
	c.merge(nil, team.values, LayerTeam, team, nil)
	if indiv != nil {
		c.merge(nil, indiv.values, LayerIndividual, indiv, locks)
	}
	c.fillElemDefaults(c.Values, schema, nil)

	data, err := json.Marshal(c.Values)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("decoding the merged configuration: %w", err)
	}
	if errs := c.check(team); len(errs) > 0 {
		return nil, errs.sorted()
	}
	return c, nil
}

func nested(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

// merge lays src over c.Values at path, recording provenance. Tables and maps merge key by key;
// every other value, hooks included, replaces what was there.
func (c *Config) merge(path Path, src map[string]any, layer string, d *doc, locks []lockPattern) {
	dst := c.Values
	for _, seg := range path {
		dst = dst[seg].(map[string]any)
	}
	for _, k := range sortedKeys(src) {
		p := path.child(k)
		v := src[k]
		n := lookupSchema(p)
		if m, ok := v.(map[string]any); ok && n != nil && (n.kind == kTable || n.kind == kMap) {
			if _, ok := dst[k].(map[string]any); !ok {
				dst[k] = map[string]any{}
			}
			c.merge(p, m, layer, d, locks)
			continue
		}
		pos := Pos{}
		if d != nil {
			pos = d.at(p)
		}
		if lock, locked := matchLock(locks, p); locked {
			c.provenance(p).Settings = append(c.provenance(p).Settings, Setting{Layer: layer, Pos: pos, Value: v, Status: "refused", Lock: lock})
			c.Refused = append(c.Refused, Refusal{Key: p.String(), Pos: pos, Lock: lock})
			continue
		}
		c.set(p, v, layer, pos)
		dst[k] = v
	}
}

func (c *Config) provenance(p Path) *Provenance {
	pr, ok := c.Provenance[p.key()]
	if !ok {
		pr = &Provenance{Key: p.String()}
		c.Provenance[p.key()] = pr
	}
	return pr
}

func (c *Config) set(p Path, v any, layer string, pos Pos) {
	pr := c.provenance(p)
	for i := range pr.Settings {
		if pr.Settings[i].Status == "effective" {
			pr.Settings[i].Status = "overridden"
		}
	}
	pr.Settings = append(pr.Settings, Setting{Layer: layer, Pos: pos, Value: v, Status: "effective"})
	pr.Value = v
}

// fillElemDefaults adds the defaults of map entries, such as a project's runner, as default-layer
// settings.
func (c *Config) fillElemDefaults(v map[string]any, n *node, path Path) {
	for _, k := range sortedKeys(v) {
		var child *node
		switch n.kind {
		case kTable:
			child = n.fields[k]
		case kMap:
			child = n.elem
		}
		sub, ok := v[k].(map[string]any)
		if child == nil || !ok || (child.kind != kTable && child.kind != kMap) {
			continue
		}
		if n.kind == kMap && child.kind == kTable {
			for dk, dv := range elemDefaults(child) {
				if _, set := sub[dk]; !set {
					sub[dk] = dv
					c.set(path.child(k).child(dk), dv, LayerDefault, Pos{})
				}
			}
		}
		c.fillElemDefaults(sub, child, path.child(k))
	}
}

// lockPattern is one entry of [locks] keys.
type lockPattern struct {
	text string
	path Path
}

func parseLocks(team *doc) ([]lockPattern, Errors) {
	raw, _ := nested(team.values, "locks", "keys").([]any)
	var locks []lockPattern
	var errs Errors
	for i, r := range raw {
		s := r.(string)
		p, err := parsePattern(s)
		where := Path{"locks", "keys"}.index(i)
		if err != nil {
			errs = append(errs, &Error{Pos: team.at(where), Key: where.String(), Msg: fmt.Sprintf("%q is not a key pattern: %v", s, err),
				Fix: `write a dotted key, using * for one segment, e.g. "resources.*.owner_preempts"`})
			continue
		}
		if !patternMatchesSchema(p) {
			errs = append(errs, &Error{Pos: team.at(where), Key: where.String(), Msg: fmt.Sprintf("%q matches no configuration key", s),
				Fix: "check the key's spelling against docs/reference/config.md"})
			continue
		}
		locks = append(locks, lockPattern{text: s, path: p})
	}
	return locks, errs
}

func parsePattern(s string) (Path, error) {
	parts := strings.Split(s, ".")
	for i, part := range parts {
		if part == "*" {
			continue
		}
		if !bareKey(part) {
			return nil, fmt.Errorf("segment %d (%q) is not a bare key or *", i+1, part)
		}
	}
	return Path(parts), nil
}

func patternMatchesSchema(p Path) bool {
	n := schema
	for _, seg := range p {
		switch n.kind {
		case kTable:
			if seg == "*" {
				return true // locks every key of the table
			}
			n = n.fields[seg]
		case kMap:
			n = n.elem
		case kHook:
			n = hookTable.fields[seg]
		case kMux:
			n = muxTable.fields[seg]
		default:
			return false
		}
		if n == nil {
			return false
		}
	}
	return true
}

func matchLock(locks []lockPattern, p Path) (string, bool) {
	for _, l := range locks {
		if len(l.path) > len(p) {
			continue
		}
		ok := true
		for i, seg := range l.path {
			if seg != "*" && seg != p[i] {
				ok = false
				break
			}
		}
		if ok {
			return l.text, true
		}
	}
	return "", false
}

// at returns the position of the effective setting of a key, or of its nearest parent.
func (c *Config) at(p Path, team *doc) Pos {
	for q := p; len(q) > 0; q = q[:len(q)-1] {
		if pr, ok := c.Provenance[q.key()]; ok {
			for _, s := range pr.Settings {
				if s.Status == "effective" && s.Pos.Line > 0 {
					return s.Pos
				}
			}
		}
		if pos, ok := team.pos[q.key()]; ok {
			return pos
		}
	}
	// The key is missing altogether: point at the top of the file.
	return Pos{File: team.file, Line: 1, Col: 1}
}

var memoryRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?\s*(MB|GB)$`)

// check validates what spans keys: required keys, references, uniqueness and cycles.
func (c *Config) check(team *doc) Errors {
	var errs Errors
	fail := func(p Path, msg, fix string) {
		errs = append(errs, &Error{Pos: c.at(p, team), Key: p.String(), Msg: msg, Fix: fix})
	}

	ws := Path{"workspace"}
	if c.Workspace.Name == "" {
		fail(ws.child("name"), "is missing", `add name = "<display name>" under [workspace]`)
	}
	if c.Workspace.Namespace == "" {
		fail(ws.child("namespace"), "is missing", `add namespace = "<short name>" under [workspace], e.g. "shop"`)
	}
	switch s := c.Workspace.Schema; {
	case s == 0:
		fail(ws.child("schema"), "is missing", fmt.Sprintf("add schema = %d under [workspace]", SchemaVersion))
	case s > SchemaVersion:
		fail(ws.child("schema"), fmt.Sprintf("is %d, but this aw reads schema %d", s, SchemaVersion),
			"upgrade the workspace's pinned aw (.aw/version) to a release that reads this schema")
	case s < 1:
		fail(ws.child("schema"), "must be 1 or more", fmt.Sprintf("set schema = %d", SchemaVersion))
	}

	if c.Lanes.Pool > c.Lanes.Max {
		fail(Path{"lanes", "pool"}, fmt.Sprintf("is %d, more than lanes.max (%d)", c.Lanes.Pool, c.Lanes.Max), "lower pool or raise max")
	}
	for _, name := range sortedMap(c.Lanes.Data) {
		d, p := c.Lanes.Data[name], Path{"lanes", "data", name}
		switch d.Provider {
		case "":
			fail(p, "has no provider", `add provider = "postgres", "redis" or "external"`)
		case "external":
			if d.Command == "" {
				fail(p.child("provider"), "is external but there is no command", `add command = "<provider executable>"`)
			}
		case "postgres":
			if len(d.Databases) == 0 {
				fail(p, "is a postgres provider with no databases", `add databases = ["<name>"]`)
			}
			if d.URL == "" {
				fail(p, "is a postgres provider with no url", `add url = "postgres://postgres@localhost:5432"`)
			}
			for i, db := range d.Databases {
				if !databaseRE.MatchString(db) {
					fail(p.child("databases").index(i), fmt.Sprintf("%q is not a plain database name", db), "use letters, digits and underscores, starting with a letter or underscore")
				}
				if db == d.Template {
					fail(p.child("databases").index(i), fmt.Sprintf("%q is the template, and nothing may connect to a template", db), "use a different name for the template or the database")
				}
			}
			if d.Template != "" && !databaseRE.MatchString(d.Template) {
				fail(p.child("template"), fmt.Sprintf("%q is not a plain database name", d.Template), "use letters, digits and underscores, starting with a letter or underscore")
			}
		case "redis":
			if d.Template != "" || len(d.Databases) > 0 {
				fail(p, "is a redis provider, which takes no template or databases", "remove template and databases")
			}
			if d.URL == "" {
				fail(p, "is a redis provider with no url", `add url = "redis://localhost:6379"`)
			}
			if c.Lanes.Max > redisDatabases-1 {
				fail(Path{"lanes", "max"}, fmt.Sprintf("is %d, but lanes use Redis databases 1 to %d by default", c.Lanes.Max, redisDatabases-1), fmt.Sprintf("lower max to %d", redisDatabases-1))
			}
		}
		if d.Provider != "external" && d.Command != "" {
			fail(p.child("command"), "applies only to provider = \"external\"", "remove it")
		}
	}

	if len(c.Projects) == 0 {
		fail(Path{"projects"}, "the workspace has no projects", "add a [projects.<name>] table for each repository")
	}
	dirs := map[string]string{}
	servers := map[string]*Server{}
	for _, name := range sortedMap(c.Projects) {
		pr, p := c.Projects[name], Path{"projects", name}
		if name == "all" {
			fail(p, `"all" is reserved: commands use it to mean every project`, "rename the project")
		}
		if pr.Directory == "" {
			pr.Directory = name
		}
		dir := pr.Directory
		clean := filepath.ToSlash(filepath.Clean(dir))
		switch {
		case filepath.IsAbs(dir):
			fail(p.child("directory"), "must be relative to the workspace", "use a path inside the workspace, e.g. \""+name+"\"")
		case slices.Contains(strings.Split(clean, "/"), ".."):
			fail(p.child("directory"), fmt.Sprintf("%q leaves the workspace", dir), "move the repository inside the workspace and name its directory, e.g. \""+name+"\"")
		case clean != dir:
			fail(p.child("directory"), fmt.Sprintf("%q is not a clean path", dir), fmt.Sprintf("write it as %q", clean))
		case dir == ".worktrees" || dir == ".aw" || dir == ".git" || strings.HasPrefix(dir, ".worktrees/") || strings.HasPrefix(dir, ".aw/") || dir == "bin":
			fail(p.child("directory"), fmt.Sprintf("%q is reserved by aw", dir), "move the repository to another directory")
		case dir == "." && len(c.Projects) > 1:
			fail(p.child("directory"), `"." makes a single-repository workspace, so it must be the only project`, "give each project its own directory, or remove the others")
		}
		if other, dup := dirs[dir]; dup {
			fail(p.child("directory"), fmt.Sprintf("%q is also project %s's directory; directories must be unique", dir, other), "give each project its own directory")
		}
		dirs[dir] = name
		for _, sname := range sortedMap(pr.Servers) {
			s, sp := pr.Servers[sname], p.child("servers").child(sname)
			servers[name+"."+sname] = s
			if s.Command == "" {
				fail(sp, "has no command", `add command = "exec <server> --port {port}"`)
			}
			if s.Port == 0 {
				fail(sp, "has no base port", "add port = <the port the owner's copy uses>")
			}
		}
		c.checkNeeds(pr.Needs, p.child("needs"), fail)
		for i, sec := range pr.Secrets {
			if clean := filepath.ToSlash(filepath.Clean(sec)); filepath.IsAbs(sec) || clean != sec || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
				fail(p.child("secrets").index(i), fmt.Sprintf("%q is not a clean path inside the project", sec), "name the file relative to the project, e.g. \".env\"")
			}
		}
		for _, gname := range sortedMap(pr.LaneGuards) {
			g, gp := pr.LaneGuards[gname], p.child("lane_guards").child(gname)
			switch {
			case g.File == "":
				fail(gp, "has no file", `add file = "<path in the branch>"`)
			case g.Matches == "":
				fail(gp, "has no matches", `add matches = "<regular expression the file must match>"`)
			case g.Message == "":
				fail(gp, "has no message", `add message = "<what is wrong with a failing branch, and the fix>"`)
			}
			if g.Matches != "" {
				if _, err := regexp.Compile(g.Matches); err != nil {
					fail(gp.child("matches"), fmt.Sprintf("is not a regular expression: %v", err), "fix the expression (Go RE2 syntax)")
				}
			}
		}
		for _, h := range []struct {
			name string
			hook *Hook
		}{{"prepare", pr.Lifecycle.Prepare}, {"setup", pr.Lifecycle.Setup}, {"reset", pr.Lifecycle.Reset}, {"restore", pr.Lifecycle.Restore}} {
			if h.hook != nil {
				c.checkNeeds(h.hook.Needs, p.child("lifecycle").child(h.name).child("needs"), fail)
			}
		}
	}
	for _, name := range sortedMap(c.Guards) {
		g, gp := c.Guards[name], Path{"guards", name}
		if len(g.Commands) == 0 && len(g.Paths) == 0 {
			fail(gp, "has no commands or paths, so it would never apply", `add commands = ["<program> <subcommand>"] or paths = ["<file pattern>"]`)
		}
		if g.Message == "" {
			fail(gp, "has no message", `add message = "<why this is refused, and what to do instead>"`)
		}
		for i, cmd := range g.Commands {
			if strings.TrimSpace(cmd) == "" {
				fail(gp.child("commands").index(i), "is empty", "name a program, and any words it must be followed by")
			}
		}
		for i, p := range g.Paths {
			if _, err := path.Match(strings.ReplaceAll(p, "**", "*"), ""); err != nil || p == "" || strings.HasPrefix(p, "/") {
				fail(gp.child("paths").index(i), fmt.Sprintf("%q is not a file pattern from the workspace root", p), `write it like "db/schema.rb" or "config/**/*.yml"`)
			}
		}
		for i, r := range g.Requires {
			if clean := filepath.ToSlash(filepath.Clean(r)); filepath.IsAbs(r) || clean != r || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
				fail(gp.child("requires").index(i), fmt.Sprintf("%q is not a clean path inside the workspace", r), `name the file from the workspace root, e.g. "bin/deploy-check"`)
			}
		}
	}
	for _, h := range []struct {
		name string
		hook *Hook
	}{{"prepare", c.Lifecycle.Prepare}, {"teardown", c.Lifecycle.Teardown}} {
		if h.hook != nil {
			c.checkNeeds(h.hook.Needs, Path{"lifecycle", h.name, "needs"}, fail)
		}
	}

	// Server order: every after entry names a server, and there is no cycle.
	deps := map[string][]string{}
	for _, project := range sortedMap(c.Projects) {
		for _, sname := range sortedMap(c.Projects[project].Servers) {
			id := project + "." + sname
			for i, a := range c.Projects[project].Servers[sname].After {
				target := a
				if !strings.Contains(a, ".") {
					target = project + "." + a
				}
				if _, ok := servers[target]; !ok {
					fail(Path{"projects", project, "servers", sname, "after"}.index(i), fmt.Sprintf("there is no server named %q", a),
						"name a server of this project, or <project>.<server>; known servers: "+strings.Join(sortedMap(servers), ", "))
					continue
				}
				deps[id] = append(deps[id], target)
			}
		}
	}
	if cycle := findCycle(deps); cycle != nil {
		project, sname, _ := strings.Cut(cycle[0], ".")
		fail(Path{"projects", project, "servers", sname, "after"}, "servers wait for each other in a cycle: "+strings.Join(cycle, " → "),
			"remove one of these after entries")
	}

	owned := map[string]string{}
	for _, name := range sortedMap(c.Resources) {
		r, p := c.Resources[name], Path{"resources", name}
		if name == "servers" {
			fail(p, `"servers" is the implicit resource for servers not listed elsewhere`, "rename the resource; set its capacity with [lanes] server_capacity")
		}
		switch r.Kind {
		case "":
			fail(p, "has no kind", `add kind = "process" or kind = "token"`)
		case "process":
			if r.Capacity == 0 {
				fail(p, "has no capacity", "add capacity = <how many copies may run at once>")
			}
			if len(r.Tokens) > 0 || r.Acquire != "" || r.Release != "" {
				fail(p, "is a process resource, so tokens, acquire and release do not apply", "remove them, or use kind = \"token\"")
			}
			for i, s := range r.Servers {
				sp := p.child("servers").index(i)
				if _, ok := servers[s]; !ok {
					fail(sp, fmt.Sprintf("there is no server named %q", s), "name servers as <project>.<server>; known servers: "+strings.Join(sortedMap(servers), ", "))
				} else if other, dup := owned[s]; dup {
					fail(sp, fmt.Sprintf("%s already belongs to resource %s; a server belongs to one resource", s, other), "list it in one resource only")
				} else {
					owned[s] = name
				}
			}
		case "token":
			if len(r.Servers) > 0 {
				fail(p.child("servers"), "a token resource owns no servers", "remove servers, or use kind = \"process\"")
			}
			switch {
			case len(r.Tokens) > 0 && r.Capacity != 0 && r.Capacity != len(r.Tokens):
				fail(p.child("capacity"), fmt.Sprintf("is %d, but %d tokens are listed", r.Capacity, len(r.Tokens)), "remove capacity: it is the number of tokens")
			case len(r.Tokens) > 0:
				r.Capacity = len(r.Tokens)
			case r.Capacity == 0:
				fail(p, "has neither tokens nor a capacity", `list tokens = ["…"], or set capacity and acquire`)
			case r.Acquire == "":
				fail(p, "has a capacity but no tokens and no acquire command", `add acquire = "<command that returns a token>"`)
			}
		}
		if r.Memory != "" && !memoryRE.MatchString(r.Memory) {
			fail(p.child("memory"), fmt.Sprintf("%q is not a memory size", r.Memory), `write it as "512MB" or "12GB"`)
		}
	}

	for _, name := range sortedMap(c.Commands) {
		cmd, p := c.Commands[name], Path{"commands", name}
		if cmd.Run == "" {
			fail(p, "has no run", `add run = "<command>"`)
		}
		if cmd.Project == "" {
			fail(p, "has no project", "add project = \"<project>\"; known projects: "+strings.Join(sortedMap(c.Projects), ", "))
		} else if _, ok := c.Projects[cmd.Project]; !ok {
			fail(p.child("project"), fmt.Sprintf("there is no project named %q", cmd.Project), "known projects: "+strings.Join(sortedMap(c.Projects), ", "))
		}
		c.checkNeeds(cmd.Needs, p.child("needs"), fail)
		for i, a := range cmd.After {
			if !c.validStep(a) {
				fail(p.child("after").index(i), fmt.Sprintf("%q is not a lifecycle step", a), `name "prepare", or <project>.install, <project>.setup or <project>.reset`)
			}
		}
	}
	return errs
}

func (c *Config) checkNeeds(needs []string, p Path, fail func(Path, string, string)) {
	for i, n := range needs {
		if _, ok := c.Resources[n]; !ok && n != "servers" {
			fail(p.index(i), fmt.Sprintf("there is no resource named %q", n), "known resources: "+strings.Join(append(sortedMap(c.Resources), "servers"), ", "))
		}
	}
}

func (c *Config) validStep(s string) bool {
	if s == "prepare" {
		return true
	}
	project, step, ok := strings.Cut(s, ".")
	_, known := c.Projects[project]
	return ok && known && (step == "install" || step == "setup" || step == "reset")
}

func findCycle(deps map[string][]string) []string {
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var found []string
	var visit func(n string) bool
	visit = func(n string) bool {
		state[n] = active
		stack = append(stack, n)
		for _, m := range deps[n] {
			switch state[m] {
			case active:
				i := slices.Index(stack, m)
				found = append(slices.Clone(stack[i:]), m)
				return true
			case unseen:
				if visit(m) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return false
	}
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if state[k] == unseen && visit(k) {
			return found
		}
	}
	return nil
}

func sortedMap[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Get returns the effective value at a key: a leaf value, or a table.
func (c *Config) Get(p Path) (any, bool) {
	var v any = c.Values
	for _, seg := range p {
		if strings.HasPrefix(seg, "[") {
			arr, ok := v.([]any)
			var i int
			if _, err := fmt.Sscanf(seg, "[%d]", &i); err != nil || !ok || i < 0 || i >= len(arr) {
				return nil, false
			}
			v = arr[i]
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return v, true
}

// Explain returns the provenance of a key, or of every leaf under it when it is a table, in key
// order. A key that is known to the schema but set by nobody returns no entries and true.
func (c *Config) Explain(p Path) ([]*Provenance, bool) {
	var out []*Provenance
	prefix := p.key()
	for k, pr := range c.Provenance {
		if k == prefix || strings.HasPrefix(k, prefix+"\x00") {
			out = append(out, pr)
		}
	}
	slices.SortFunc(out, func(a, b *Provenance) int { return strings.Compare(a.Key, b.Key) })
	if len(out) == 0 {
		return nil, lookupSchema(p) != nil
	}
	return out, true
}

func sourceOf(path string) Source {
	s := Source{Path: path}
	if info, err := os.Stat(path); err == nil {
		s.Exists, s.Size, s.ModTime, s.Inode = true, info.Size(), info.ModTime().UnixNano(), inode(info)
	}
	return s
}
