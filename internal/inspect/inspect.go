// Package inspect gathers facts about a workspace's projects, deterministically and without running
// anything (onboarding.md, "Facts first"), and the findings of the readiness audit that a program can
// check (R89). It only reads files.
package inspect

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Project is what could be found out about one project.
type Project struct {
	Name         string            `json:"name"`
	Directory    string            `json:"directory"`
	Stacks       []string          `json:"stacks"`
	Lockfiles    []string          `json:"lockfiles"`
	Scripts      map[string]string `json:"scripts,omitempty"`
	Procfile     map[string]string `json:"procfile,omitempty"`
	Compose      []string          `json:"compose,omitempty"`
	Devcontainer bool              `json:"devcontainer"`
	Mobile       []string          `json:"mobile,omitempty"`
	EnvVars      []string          `json:"env_vars,omitempty"`
	Ports        []int             `json:"ports,omitempty"`
	Findings     []Finding         `json:"findings"`
}

// Finding is one thing a project must change to work well in lanes, with its fix.
type Finding struct {
	Kind    string `json:"kind"`
	Project string `json:"project"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

var skipDirs = []string{".git", ".worktrees", "node_modules", "vendor", "dist", "build", "tmp", "target", ".venv", "log", "coverage"}

var sourceExt = []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".rb", ".py", ".go", ".java", ".kt", ".php", ".rs", ".yml", ".yaml", ".toml", ".json", ".erb", ".env", ".sh"}

var (
	hardcodedPort = regexp.MustCompile(`\b(?:localhost|127\.0\.0\.1):(\d{2,5})\b`)
	envRead       = regexp.MustCompile(`process\.env\.([A-Z_][A-Z0-9_]*)|ENV\[['"]([A-Z_][A-Z0-9_]*)['"]\]|ENV\.fetch\(['"]([A-Z_][A-Z0-9_]*)['"]|os\.Getenv\("([A-Z_][A-Z0-9_]*)"\)|os\.environ(?:\.get\(|\[)['"]([A-Z_][A-Z0-9_]*)['"]`)
	fixedDatabase = regexp.MustCompile(`^\s*database:\s*([A-Za-z0-9_.-]+)\s*$`)
	fixedRedis    = regexp.MustCompile(`redis://[^\s'"]*/\d+\b`)
	cookieDomain  = regexp.MustCompile(`(?i)\bdomain['"]?\s*[:=]>?\s*['"]\.?localhost['"]`)
	idempotency   = regexp.MustCompile(`(?i)idempotency[_-]?key`)
	absolutePath  = regexp.MustCompile(`['"=:\s](/(?:home|Users)/[A-Za-z0-9._-]+/)`)
	secretPrinted = regexp.MustCompile(`(?i)(console\.log|puts|print|logger\.\w+)\(?.*(secret|api_?key|password)`)
)

// Scan inspects one project's directory.
func Scan(name, dir, rel string) (*Project, error) {
	p := &Project{Name: name, Directory: rel, Findings: []Finding{}}
	exists := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	for file, stack := range map[string]string{"package.json": "node", "Gemfile": "ruby", "go.mod": "go", "pyproject.toml": "python", "requirements.txt": "python", "Cargo.toml": "rust", "composer.json": "php", "pom.xml": "java"} {
		if exists(file) && !slices.Contains(p.Stacks, stack) {
			p.Stacks = append(p.Stacks, stack)
		}
	}
	slices.Sort(p.Stacks)
	for _, f := range []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "Gemfile.lock", "go.sum", "poetry.lock", "uv.lock", "Cargo.lock", "composer.lock"} {
		if exists(f) {
			p.Lockfiles = append(p.Lockfiles, f)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			p.Scripts = pkg.Scripts
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "Procfile")); err == nil {
		p.Procfile = map[string]string{}
		for _, line := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(k, "#") {
				p.Procfile[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	for _, f := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		if exists(f) {
			p.Compose = append(p.Compose, f)
		}
	}
	p.Devcontainer = exists(".devcontainer/devcontainer.json") || exists(".devcontainer.json")
	if exists("android/build.gradle") || exists("build.gradle.kts") || exists("settings.gradle.kts") || exists("android") {
		p.Mobile = append(p.Mobile, "android")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.xcodeproj")); len(matches) > 0 || exists("ios") {
		p.Mobile = append(p.Mobile, "ios")
	}
	env, ports := map[string]bool{}, map[int]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 512*1024 || !source(d.Name()) {
			return nil
		}
		relFile, _ := filepath.Rel(dir, path)
		scanFile(p, path, filepath.ToSlash(relFile), env, ports)
		return nil
	})
	p.EnvVars = slices.Sorted(keysOf(env))
	for port := range ports {
		p.Ports = append(p.Ports, port)
	}
	slices.Sort(p.Ports)
	return p, err
}

func keysOf(m map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func source(name string) bool {
	if strings.HasPrefix(name, ".env") {
		return true
	}
	return slices.Contains(sourceExt, filepath.Ext(name))
}

func isTest(file string) bool {
	return strings.Contains(file, "test") || strings.Contains(file, "spec") || strings.Contains(file, "fixture")
}

func scanFile(p *Project, path, file string, env map[string]bool, ports map[int]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	add := func(n int, kind, message, fix string) {
		p.Findings = append(p.Findings, Finding{Kind: kind, Project: p.Name, File: file, Line: n, Message: message, Fix: fix})
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		for _, m := range envRead.FindAllStringSubmatch(line, -1) {
			for _, g := range m[1:] {
				if g != "" {
					env[g] = true
				}
			}
		}
		if isTest(file) {
			continue
		}
		if m := hardcodedPort.FindStringSubmatch(line); m != nil && !strings.Contains(line, "ENV") && !strings.Contains(line, "process.env") {
			port := 0
			for _, c := range m[1] {
				port = port*10 + int(c-'0')
			}
			ports[port] = true
			add(n, "hardcoded-port", m[0]+" is written into the code, so two lanes would use the same port", "read the port from the environment; aw gives each lane its own")
		}
		if m := fixedDatabase.FindStringSubmatch(line); m != nil && strings.HasSuffix(file, "database.yml") {
			add(n, "fixed-database", "the database name "+m[1]+" does not vary by lane", "make it depend on AW_LANE, for example "+m[1]+`<%= ENV["AW_LANE"] %>`)
		}
		if fixedRedis.MatchString(line) && !strings.Contains(line, "ENV") {
			add(n, "fixed-redis", "a Redis database number is written into the code", "read the Redis URL from the environment")
		}
		if cookieDomain.MatchString(line) {
			add(n, "cookie-domain", "cookies are scoped to localhost, so lanes on different ports share them", "scope them to the lane's hostname, AW_HOSTNAME")
		}
		if idempotency.MatchString(line) && strings.Contains(strings.ToLower(line), "id") && !strings.Contains(strings.ToLower(line), "created") && !strings.Contains(strings.ToLower(line), "time") && !strings.Contains(strings.ToLower(line), "uuid") {
			add(n, "idempotency-key", "an idempotency key built from a record's ID can repeat across lanes restored from one snapshot", "add something unique to the record's lifetime, such as its creation time")
		}
		if m := absolutePath.FindStringSubmatch(line); m != nil {
			add(n, "absolute-path", m[1]+" names one machine's path, which differs between host and container", "derive it from the project's own directory")
		}
		if secretPrinted.MatchString(line) {
			add(n, "secret-logged", "this looks like it prints a secret", "do not log it; tools that need a secret read it themselves")
		}
	}
}
