package runners

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// UsesGeneratedConfig selects Mode A only for a project without either existing wiring mode.
func UsesGeneratedConfig(j devcontainerJSON, local string) bool {
	return local == "" && !slices.Contains(j.composeFiles(), Fragment)
}

// generateOverlay preserves unknown settings as raw JSON. The listed Compose files stay at their
// original paths, so their relative build contexts, env files and volumes keep the same base.
func (d *Devcontainer) generateOverlay(j devcontainerJSON) ([]byte, error) {
	fields := maps.Clone(j.raw)
	fail := func(key string) error {
		return fmt.Errorf("devcontainer.json %s cannot be preserved in the generated configuration → fix %s in the project's devcontainer.json", key, key)
	}
	files := []string{}
	for _, file := range j.composeFiles() {
		file = strings.ReplaceAll(file, "${localWorkspaceFolder}", d.Dir)
		if strings.Contains(file, "${") {
			return nil, fail("dockerComposeFile (use literal paths or ${localWorkspaceFolder})")
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(d.Dir, ".devcontainer", file)
		}
		files = append(files, file)
	}
	files = append(files, filepath.Join(d.Dir, ".devcontainer", d.fragment))
	fields["dockerComposeFile"], _ = json.Marshal(files)
	name := d.Project
	if raw, ok := fields["name"]; ok {
		if json.Unmarshal(raw, &name) != nil {
			return nil, fail("name")
		}
	}
	fields["name"], _ = json.Marshal(name + " (aw)")
	// Local feature identifiers resolve beside devcontainer.json too.
	if raw, ok := fields["features"]; ok {
		var features map[string]json.RawMessage
		if json.Unmarshal(raw, &features) != nil {
			return nil, fail("features")
		}
		relocated := map[string]json.RawMessage{}
		for key, value := range features {
			if strings.HasPrefix(key, "./") || strings.HasPrefix(key, "../") {
				key = filepath.ToSlash(filepath.Join("..", key))
			}
			relocated[key] = value
		}
		fields["features"], _ = json.Marshal(relocated)
	}
	if raw, ok := fields["forwardPorts"]; ok {
		var ports []json.RawMessage
		if json.Unmarshal(raw, &ports) != nil {
			return nil, fail("forwardPorts")
		}
		kept := []json.RawMessage{}
		for _, port := range ports {
			value := string(port)
			var text string
			if json.Unmarshal(port, &text) == nil {
				value = strings.TrimPrefix(text, d.Service+":")
			}
			number, err := strconv.Atoi(value)
			if err != nil || !slices.Contains(d.Ports, number) {
				kept = append(kept, port)
			}
		}
		fields["forwardPorts"], _ = json.Marshal(kept)
	}
	// Prevent automatic forwarding of ports aw already publishes, preserving labels and other rules.
	attributes := map[string]json.RawMessage{}
	if raw, ok := fields["portsAttributes"]; ok {
		if json.Unmarshal(raw, &attributes) != nil || attributes == nil {
			return nil, fail("portsAttributes")
		}
	}
	for _, port := range d.Ports {
		key := strconv.Itoa(port)
		settings := map[string]json.RawMessage{}
		if raw, ok := attributes[key]; ok {
			if json.Unmarshal(raw, &settings) != nil || settings == nil {
				return nil, fail("portsAttributes." + key)
			}
		}
		settings["onAutoForward"] = json.RawMessage(`"ignore"`)
		attributes[key], _ = json.Marshal(settings)
	}
	if len(attributes) > 0 {
		fields["portsAttributes"], _ = json.Marshal(attributes)
	}
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot generate %s: %w → fix the project's devcontainer.json", GeneratedConfig, err)
	}
	return append(append([]byte(generatedHeader), data...), '\n'), nil
}

const generatedHeader = "// Written by aw; do not edit.\n"

// A project may already have an aw configuration directory of its own. Automatic Mode A must
// never overwrite that configuration, or follow a link out of its generated directory.
func (d *Devcontainer) checkGeneratedFiles() error {
	dir := filepath.Join(d.Dir, ".devcontainer", "aw")
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link → move it aside or use local_compose", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot inspect %s: %w → make the directory readable", dir, err)
	}
	for _, file := range []struct{ name, prefix string }{{"devcontainer.json", generatedHeader}, {Fragment, "# Written by aw;"}} {
		path := filepath.Join(dir, file.name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect %s: %w → make the directory readable", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link → move it aside or use local_compose", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w → make it readable", path, err)
		}
		if !bytes.HasPrefix(data, []byte(file.prefix)) {
			return fmt.Errorf("%s already exists and was not generated by aw → move it aside or use local_compose", path)
		}
	}
	return nil
}

func writeOverlay(name string, data []byte) error {
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w → make its parent directory writable", name, err)
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".devcontainer-*.json")
	if err != nil {
		return fmt.Errorf("cannot write %s: %w → make its directory writable", name, err)
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o644); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), name)
	}
	if err != nil {
		return fmt.Errorf("cannot write %s: %w → make its directory writable", name, err)
	}
	return nil
}
