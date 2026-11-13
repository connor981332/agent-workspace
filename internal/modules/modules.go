// Package modules installs first-party modules into a workspace and records what they installed
// (architecture.md, "Modules"). Installed files are managed: their hashes are in .aw/managed, and a
// hand edit is reported instead of overwritten.
package modules

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	content "github.com/connor981332/agent-workspace/modules"

	"github.com/connor981332/agent-workspace/internal/upgrade"
)

// InstalledPath lists a workspace's modules, one per line.
const InstalledPath = ".aw/modules"

// Manifest is a module's module.toml.
type Manifest struct {
	Name         string   `toml:"-"`
	Summary      string   `toml:"summary"`
	Services     []string `toml:"services"`
	Ports        []int    `toml:"ports"`
	Instructions string   `toml:"instructions"`
	Fragment     string   `toml:"fragment"`
	Gitignore    []string `toml:"gitignore"`
}

// Available lists the first-party modules, by name.
func Available() ([]Manifest, error) {
	entries, err := content.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if e.IsDir() {
			m, err := Read(e.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// Read returns one module's manifest.
func Read(name string) (Manifest, error) {
	var m Manifest
	data, err := content.FS.ReadFile(name + "/module.toml")
	if err != nil {
		return m, fmt.Errorf("there is no module named %q → aw modules lists them", name)
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("module %s: %w", name, err)
	}
	m.Name = name
	return m, nil
}

// Installed returns the workspace's module names.
func Installed(owner string) []string {
	data, _ := os.ReadFile(filepath.Join(owner, InstalledPath))
	return strings.Fields(string(data))
}

// Services are the long-running services of the installed modules, and Ports their published ports.
func Services(owner string) (services []string, ports []int) {
	for _, name := range Installed(owner) {
		if m, err := Read(name); err == nil {
			services = append(services, m.Services...)
			ports = append(ports, m.Ports...)
		}
	}
	slices.Sort(services)
	return services, ports
}

var portRE = regexp.MustCompile(`\{\{PORT:(\d+)\}\}`)

// render fills in a file's placeholders. A port becomes a Compose variable with its default, which
// aw infra sets from [ports] offset, so that the committed file holds nothing machine-specific.
func render(text, namespace, title string) string {
	text = strings.NewReplacer("{{NAME}}", namespace, "{{TITLE}}", title).Replace(text)
	return portRE.ReplaceAllString(text, "$${AW_PORT_$1:-$1}")
}

// Result says what installing did.
type Result struct {
	Files     []string
	Gitignore []string
}

type planned struct {
	path string
	data []byte
	mode fs.FileMode
}

func walk(name, namespace, title string) ([]planned, error) {
	var out []planned
	root := name + "/files"
	if _, err := content.FS.ReadDir(root); err != nil {
		return nil, nil // a module may install no files
	}
	err := fs.WalkDir(content.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := content.FS.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.NewReplacer("{{NAME}}", namespace).Replace(strings.TrimPrefix(path, root+"/"))
		mode := fs.FileMode(0o644)
		if strings.HasPrefix(string(data), "#!") {
			mode = 0o755
		}
		out = append(out, planned{rel, []byte(render(string(data), namespace, title)), mode})
		return nil
	})
	return out, err
}

// Install adds a module to the workspace. Nothing is written if a file it would install is already
// there, because that file is someone's.
func Install(owner, namespace, title, name string) (*Result, error) {
	m, err := Read(name)
	if err != nil {
		return nil, err
	}
	if slices.Contains(Installed(owner), name) {
		return nil, fmt.Errorf("module %s is already installed", name)
	}
	files, err := walk(name, namespace, title)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(owner, f.path)); err == nil {
			return nil, fmt.Errorf("%s already exists, and module %s would replace it; nothing was installed → move it aside, and run this again", f.path, name)
		}
	}
	res := &Result{Gitignore: m.Gitignore}
	for _, f := range files {
		path := filepath.Join(owner, f.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, f.data, f.mode); err != nil {
			return nil, err
		}
		if err := upgrade.Record(owner, f.path, upgrade.Hash(f.data)); err != nil {
			return nil, err
		}
		res.Files = append(res.Files, f.path)
	}
	if m.Fragment != "" {
		data, err := content.FS.ReadFile(name + "/" + m.Fragment)
		if err != nil {
			return nil, err
		}
		if err := appendText(filepath.Join(owner, "workspace.toml"), fmt.Sprintf("\n# Added by the %s module.\n%s", name, render(string(data), namespace, title))); err != nil {
			return nil, err
		}
	}
	if m.Instructions != "" {
		data, err := content.FS.ReadFile(name + "/" + m.Instructions)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(owner, ".aw"), 0o755); err != nil {
			return nil, err
		}
		if err := appendText(filepath.Join(owner, ".aw", "instructions.md"), "\n"+render(string(data), namespace, title)); err != nil {
			return nil, err
		}
	}
	return res, appendText(filepath.Join(owner, InstalledPath), name+"\n")
}

func appendText(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

// Refresh rewrites the installed modules' managed files from this release. A file edited by hand is
// left alone, listed in the result unless take says to replace it. An ejected file, which has no
// recorded hash, is the team's.
func Refresh(owner, namespace, title string, take []string) (edited []string, err error) {
	for _, name := range Installed(owner) {
		files, err := walk(name, namespace, title)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			recorded := upgrade.Recorded(owner, f.path)
			taken := slices.Contains(take, f.path)
			if recorded == "" && !taken {
				continue // ejected, or never recorded: the team's, unless --take says otherwise
			}
			path := filepath.Join(owner, f.path)
			have, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil && upgrade.Hash(have) != recorded && !taken {
				edited = append(edited, f.path)
				continue
			}
			if err := os.WriteFile(path, f.data, f.mode); err != nil {
				return nil, err
			}
			if err := upgrade.Record(owner, f.path, upgrade.Hash(f.data)); err != nil {
				return nil, err
			}
		}
	}
	return edited, nil
}

// Paths are the files the installed modules generate, relative to the owner checkout.
func Paths(owner, namespace, title string) ([]string, error) {
	var out []string
	for _, name := range Installed(owner) {
		files, err := walk(name, namespace, title)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out = append(out, f.path)
		}
	}
	return out, nil
}

// Eject stops managing a module's files, or one file. It returns the paths released.
func Eject(owner, namespace, title, target string) ([]string, error) {
	if slices.Contains(Installed(owner), target) {
		files, err := walk(target, namespace, title)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, f := range files {
			if upgrade.Recorded(owner, f.path) != "" {
				if err := upgrade.Record(owner, f.path, ""); err != nil {
					return nil, err
				}
				out = append(out, f.path)
			}
		}
		return out, nil
	}
	if upgrade.Recorded(owner, target) == "" {
		return nil, fmt.Errorf("%s is neither an installed module nor a managed file → aw modules lists what is installed", target)
	}
	return []string{target}, upgrade.Record(owner, target, "")
}
