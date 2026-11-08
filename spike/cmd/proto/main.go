// Command proto is the T3 prototype of aw's hot path (plans/release-candidate.md §12, T3). It is
// throwaway code: it exists to measure, not to be built on.
//
//	proto setup <workspace>   build a fixture state database and config cache
//	proto hook claude pre-tool-use < payload.json
//
// The hook does what speed.md allows a guard decision to do: find the workspace from the payload's
// cwd by reading .git files, check the config cache's source metadata, decode the cache, read the
// session's attachment and pending notifications from SQLite, parse the shell command, and print a
// Claude Code decision.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(hook())
	}
	if len(os.Args) == 3 && os.Args[1] == "setup" {
		if err := setup(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: proto setup <workspace> | proto hook <harness> <event>")
	os.Exit(2)
}

type payload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Event     string `json:"hook_event_name"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// phases records the time of each step, printed with AW_SPIKE_TIMINGS=1.
type phases struct {
	start time.Time
	last  time.Time
	out   []string
}

func (p *phases) mark(name string) {
	now := time.Now()
	p.out = append(p.out, fmt.Sprintf("%s=%d", name, now.Sub(p.last).Microseconds()))
	p.last = now
}

func hook() int {
	p := &phases{start: time.Now()}
	p.last = p.start
	decision, reason, err := decide(p)
	if err != nil {
		decision, reason = "deny", "aw: "+err.Error()
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		return 2
	}
	p.mark("output")
	if os.Getenv("AW_SPIKE_TIMINGS") == "1" {
		fmt.Fprintf(os.Stderr, "timings_us %s total=%d\n", strings.Join(p.out, " "), time.Since(p.start).Microseconds())
	}
	return 0
}

func decide(p *phases) (string, string, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", "", err
	}
	var in payload
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", "", fmt.Errorf("unrecognized payload: %v", err)
	}
	p.mark("payload")

	common, err := commonGitDir(in.Cwd)
	if err != nil {
		return "", "", err
	}
	p.mark("gitdir")

	cfg, err := loadCache(filepath.Join(common, "aw", "config.cache"))
	if err != nil {
		return "", "", err
	}
	p.mark("cache")

	lane, notes, err := readState(filepath.Join(common, "aw"), in.SessionID)
	if err != nil {
		return "", "", err
	}
	p.mark("sqlite")

	words, err := commandWords(in.ToolInput.Command)
	if err != nil {
		return "", "", err
	}
	p.mark("parse")

	for _, w := range words {
		for _, g := range cfg.Guards {
			if w == g.Command {
				return "deny", g.Message, nil
			}
		}
	}
	return "allow", fmt.Sprintf("lane %s, %d notifications", lane, notes), nil
}

// commonGitDir finds the repository's common Git directory from dir by reading .git files and
// commondir, as gitx will, with no subprocess.
func commonGitDir(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		dotgit := filepath.Join(d, ".git")
		info, err := os.Lstat(dotgit)
		if err == nil {
			if info.IsDir() {
				return dotgit, nil
			}
			data, err := os.ReadFile(dotgit)
			if err != nil {
				return "", err
			}
			gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
			if !ok {
				return "", fmt.Errorf("%s: not a gitdir file", dotgit)
			}
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(d, gitdir)
			}
			common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
			if err != nil {
				return gitdir, nil
			}
			c := strings.TrimSpace(string(common))
			if !filepath.IsAbs(c) {
				c = filepath.Join(gitdir, c)
			}
			return filepath.Clean(c), nil
		}
		if d == filepath.Dir(d) {
			return "", errors.New("not inside a workspace")
		}
	}
}

// Config stands in for the compiled configuration: realistic size and shape, with provenance.
type Config struct {
	Workspace  map[string]string
	Projects   []Project
	Resources  []Resource
	Guards     []Guard
	Provenance map[string]Source
}

type Project struct {
	Name, Repo, Runner, Install, Run string
	Needs                            []string
	Env                              map[string]string
}

type Resource struct {
	Name, Kind  string
	Capacity    int
	Ports       []int
	OwnerPort   int
	EvictGrace  time.Duration
	MaxHold     time.Duration
	IdleExpiry  time.Duration
	Start, Stop string
}

type Guard struct{ Command, Message string }

type Source struct {
	File       string
	Line, Col  int
	Layer      string
	Overridden bool
}

// The cache file: a header of source metadata (path, size, mtime) that is checked with stat
// before the body is decoded, then the gob-encoded body.
type cacheHeader struct {
	Sources []sourceMeta
}

type sourceMeta struct {
	Path  string
	Size  int64
	MTime int64
}

func loadCache(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, errors.New("config cache: truncated")
	}
	n := binary.LittleEndian.Uint32(data)
	if int(n) > len(data)-4 {
		return nil, errors.New("config cache: bad header")
	}
	var hdr cacheHeader
	if err := json.Unmarshal(data[4:4+n], &hdr); err != nil {
		return nil, err
	}
	for _, s := range hdr.Sources {
		info, err := os.Stat(s.Path)
		if err != nil || info.Size() != s.Size || info.ModTime().UnixNano() != s.MTime {
			return nil, fmt.Errorf("config cache is stale (%s changed)", s.Path)
		}
	}
	var cfg Config
	body := data[4+n:]
	if os.Getenv("AW_SPIKE_CACHE") == "json" {
		err = json.Unmarshal(body, &cfg)
	} else {
		err = gob.NewDecoder(bytes.NewReader(body)).Decode(&cfg)
	}
	return &cfg, err
}

// commandWords parses a shell command and returns the literal words of every simple command.
func commandWords(cmd string) ([]string, error) {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil, fmt.Errorf("cannot parse the command: %v", err)
	}
	var words []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			for _, w := range call.Args {
				if lit := w.Lit(); lit != "" {
					words = append(words, lit)
				}
			}
		}
		return true
	})
	return words, nil
}

func setup(ws string) error {
	common, err := commonGitDir(ws)
	if err != nil {
		return err
	}
	dir := filepath.Join(common, "aw")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if err := setupState(dir); err != nil {
		return err
	}

	// A realistic merged configuration: 4 projects, 6 resources, 40 guards, provenance for every
	// key.
	cfg := Config{Workspace: map[string]string{"name": "Shop", "namespace": "shop", "forge": "github"}, Provenance: map[string]Source{}}
	for i, name := range []string{"web", "api", "android", "ios"} {
		p := Project{Name: name, Repo: "git@github.com:shop/" + name + ".git", Runner: "devcontainer",
			Install: "npm ci", Run: "npx playwright test", Needs: []string{"web-stack"}, Env: map[string]string{}}
		for j := 0; j < 20; j++ {
			p.Env[fmt.Sprintf("VAR_%d", j)] = fmt.Sprintf("value-%d-%d", i, j)
		}
		cfg.Projects = append(cfg.Projects, p)
	}
	for i := 0; i < 6; i++ {
		cfg.Resources = append(cfg.Resources, Resource{Name: fmt.Sprintf("res-%d", i), Kind: "process", Capacity: 2,
			Ports: []int{3000 + i, 4000 + i}, OwnerPort: 3000 + i, EvictGrace: 10 * time.Second,
			MaxHold: time.Hour, IdleExpiry: 30 * time.Minute, Start: "bin/rails server", Stop: "kill"})
	}
	for i := 0; i < 40; i++ {
		cfg.Guards = append(cfg.Guards, Guard{Command: fmt.Sprintf("forbidden-%d", i), Message: "use aw serve instead → aw serve web"})
	}
	for i := 0; i < 400; i++ {
		cfg.Provenance[fmt.Sprintf("projects.web.env.KEY_%d", i)] = Source{File: filepath.Join(ws, "workspace.toml"), Line: i + 1, Col: 1, Layer: "team"}
	}

	src := filepath.Join(ws, "workspace.toml")
	if err := os.WriteFile(src, []byte("[workspace]\nname = \"Shop\"\n"), 0o644); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	hdr, _ := json.Marshal(cacheHeader{Sources: []sourceMeta{{Path: src, Size: info.Size(), MTime: info.ModTime().UnixNano()}}})
	var body bytes.Buffer
	if os.Getenv("AW_SPIKE_CACHE") == "json" {
		err = json.NewEncoder(&body).Encode(cfg)
	} else {
		err = gob.NewEncoder(&body).Encode(cfg)
	}
	if err != nil {
		return err
	}
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(hdr)))
	out = append(append(out, hdr...), body.Bytes()...)
	fmt.Printf("config cache: %d bytes\n", len(out))
	return os.WriteFile(filepath.Join(dir, "config.cache"), out, 0o600)
}
