// Package sandbox configures the sandboxes of the harnesses that have one, so that a lane's agents
// can write exactly the lane, the state directory and the repositories' Git directories (plan risk
// 7). The paths are this machine's, so the files are local to a checkout and never committed. aw
// does not turn a sandbox on: that stays the user's choice.
package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Paths of the two files, relative to a lane root or the owner checkout.
const (
	ClaudeFile = ".claude/settings.local.json"
	CodexFile  = ".codex/config.toml"
)

// Files are the sandbox files aw may leave in a lane root.
var Files = []string{ClaudeFile, CodexFile}

const codexMarker = "# Managed by aw: this checkout's sandbox roots."

// Result says what happened to one file.
type Result struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Note    string `json:"note,omitempty"` // why aw left it alone
}

// Install writes both harnesses' writable paths in root. Claude Code's file is merged, keeping
// everything else in it. Codex's is aw's own, and one that a person wrote is left alone.
func Install(root string, writable []string) ([]Result, error) {
	writable = slices.Compact(slices.Sorted(slices.Values(writable)))
	c, err := installClaude(root, writable)
	if err != nil {
		return nil, err
	}
	x, err := installCodex(root, writable)
	if err != nil {
		return nil, err
	}
	return []Result{c, x}, nil
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func installClaude(root string, writable []string) (Result, error) {
	res := Result{Path: ClaudeFile}
	path := filepath.Join(root, filepath.FromSlash(ClaudeFile))
	settings := map[string]any{}
	old, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(old, &settings); err != nil {
			res.Note = fmt.Sprintf("it is not valid JSON (%v), so aw left it alone", err)
			return res, nil
		}
	} else if !os.IsNotExist(err) {
		return res, err
	}
	sb, _ := settings["sandbox"].(map[string]any)
	if sb == nil {
		sb = map[string]any{}
	}
	fs, _ := sb["filesystem"].(map[string]any)
	if fs == nil {
		fs = map[string]any{}
	}
	have, _ := fs["allowWrite"].([]any)
	for _, w := range writable {
		if !slices.Contains(have, any(w)) {
			have = append(have, w)
		}
	}
	fs["allowWrite"] = have
	sb["filesystem"] = fs
	settings["sandbox"] = sb
	// A lane root reaches each project through a link to a worktree outside it, and Claude Code asks
	// about every path that resolves outside its working directory.
	perms, _ := settings["permissions"].(map[string]any)
	dirs, _ := perms["additionalDirectories"].([]any)
	before := len(dirs)
	for _, w := range writable {
		if filepath.Base(w) != ".git" && !slices.Contains(dirs, any(w)) {
			dirs = append(dirs, w)
		}
	}
	if len(dirs) > before {
		if perms == nil {
			perms = map[string]any{}
		}
		perms["additionalDirectories"] = dirs
		settings["permissions"] = perms
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return res, err
	}
	if bytes.Equal(old, buf.Bytes()) {
		return res, nil
	}
	res.Changed = true
	return res, write(path, buf.Bytes())
}

func codexText(writable []string) []byte {
	quoted := make([]string, len(writable))
	for i, w := range writable {
		quoted[i] = strconv.Quote(w)
	}
	return []byte(codexMarker + " aw setup and lane creation rewrite it.\n\n[sandbox_workspace_write]\nwritable_roots = [" + strings.Join(quoted, ", ") + "]\n")
}

func installCodex(root string, writable []string) (Result, error) {
	res := Result{Path: CodexFile}
	path := filepath.Join(root, filepath.FromSlash(CodexFile))
	want := codexText(writable)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, want):
		return res, nil
	case err == nil && !bytes.HasPrefix(old, []byte(codexMarker)):
		res.Note = "someone else's file, so aw left it alone: add these to sandbox_workspace_write.writable_roots: " + strings.Join(writable, ", ")
		return res, nil
	case err != nil && !os.IsNotExist(err):
		return res, err
	}
	res.Changed = true
	return res, write(path, want)
}

// Remove deletes what aw wrote in a lane root that is going away or being recycled. Claude Code's
// file there is aw's own, because a lane root's copy is untracked and aw made it.
func Remove(root string) {
	os.Remove(filepath.Join(root, filepath.FromSlash(ClaudeFile)))
	if old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(CodexFile))); err == nil && bytes.HasPrefix(old, []byte(codexMarker)) {
		os.Remove(filepath.Join(root, filepath.FromSlash(CodexFile)))
	}
	for _, dir := range []string{".claude", ".codex"} {
		os.Remove(filepath.Join(root, dir)) // only if empty
	}
}

// Ours lists, for checking what ignored files a lane root holds, the sandbox files and any of their
// directories that hold nothing else: Git reports a wholly ignored directory as one entry.
func Ours(root string) []string {
	out := slices.Clone(Files)
	for _, f := range Files {
		dir := filepath.Dir(filepath.FromSlash(f))
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err == nil && len(entries) == 1 && entries[0].Name() == filepath.Base(f) {
			out = append(out, dir+"/")
		}
	}
	return out
}

// Missing reports the writable paths the Codex configuration in root does not list, for aw doctor.
func Missing(root string, writable []string) []string {
	data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(CodexFile)))
	var out []string
	for _, w := range writable {
		if !bytes.Contains(data, []byte(strconv.Quote(w))) {
			out = append(out, w)
		}
	}
	return out
}

// Exclude keeps the files out of Git in every worktree of the repository at dir, through the
// exclude file they share, which no commit carries.
func Exclude(dir string) error {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return nil // a workspace kept as plain files has no Git to keep them out of
	}
	path := filepath.Join(dir, ".git", "info", "exclude")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(old), "\n")
	var add strings.Builder
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		add.WriteByte('\n')
	}
	changed := false
	for _, f := range Files {
		if !slices.Contains(lines, "/"+f) {
			add.WriteString("/" + f + "\n")
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Lane claims share this file. Rewriting a stale snapshot can erase another claim's
	// exclusions (and make aw's local records look like the lane's own tracked work).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(add.String())
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// RehearseCommand is the one command aw onboard lets its lane's agent run without a prompt.
const RehearseCommand = "bin/aw rehearse"

const rehearseRule = "Bash(" + RehearseCommand + ")"

// AllowRehearse adds or removes the rule in a lane's own Claude Code settings. A harness's permission
// check cannot see that a rehearsal is isolated, and the onboarding loop depends on running it. The
// file is aw's, untracked and local to the lane, so the grant ends with the lane.
func AllowRehearse(root string, allow bool) error {
	path := filepath.Join(root, filepath.FromSlash(ClaudeFile))
	old, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !allow {
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	settings := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &settings); err != nil {
			return nil // not valid JSON: Install has already said it left this file alone
		}
	}
	perms, _ := settings["permissions"].(map[string]any)
	rules, _ := perms["allow"].([]any)
	has := slices.Contains(rules, any(rehearseRule))
	if has == allow {
		return nil
	}
	if allow {
		if perms == nil {
			perms = map[string]any{}
		}
		rules = append(rules, rehearseRule)
	} else {
		rules = slices.DeleteFunc(slices.Clone(rules), func(r any) bool { return r == any(rehearseRule) })
	}
	if len(rules) == 0 {
		delete(perms, "allow")
	} else {
		perms["allow"] = rules
	}
	if len(perms) == 0 {
		delete(settings, "permissions")
	} else {
		settings["permissions"] = perms
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return err
	}
	return write(path, buf.Bytes())
}
