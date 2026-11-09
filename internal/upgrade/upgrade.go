// Package upgrade holds what aw upgrade works out: version order, the changelog between two versions,
// configuration migrations, and the hashes of managed files (distribution.md, "Upgrades").
package upgrade

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ManagedPath lists the hashes of the files aw manages.
const ManagedPath = ".aw/managed"

//go:embed changelog.md
var changelog string

// Compare orders two exact versions by semantic-versioning precedence: <0, 0 or >0.
func Compare(a, b string) int {
	aCore, aPre, _ := strings.Cut(strings.SplitN(a, "+", 2)[0], "-")
	bCore, bPre, _ := strings.Cut(strings.SplitN(b, "+", 2)[0], "-")
	aParts, bParts := strings.Split(aCore, "."), strings.Split(bCore, ".")
	for i := 0; i < 3 && i < len(aParts) && i < len(bParts); i++ {
		x, _ := strconv.Atoi(aParts[i])
		y, _ := strconv.Atoi(bParts[i])
		if x != y {
			return x - y
		}
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1 // a release comes after its pre-releases
	case bPre == "":
		return -1
	}
	as, bs := strings.Split(aPre, "."), strings.Split(bPre, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		x, xErr := strconv.Atoi(as[i])
		y, yErr := strconv.Atoi(bs[i])
		switch {
		case xErr == nil && yErr == nil:
			return x - y
		case xErr == nil:
			return -1 // numbers come before words
		case yErr == nil:
			return 1
		}
		return strings.Compare(as[i], bs[i])
	}
	return len(as) - len(bs)
}

// Between returns the changelog's sections for versions after from, up to and including to, and the
// breaking lines among them.
func Between(text, from, to string) (notes string, breaking []string) {
	var out []string
	on := false
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(line, "## "); ok {
			v = strings.TrimSpace(v)
			on = Compare(v, from) > 0 && Compare(v, to) <= 0
		} else if strings.HasPrefix(line, "# ") {
			on = false
		}
		if !on {
			continue
		}
		out = append(out, line)
		if b, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")), "**Breaking:**"); ok {
			breaking = append(breaking, strings.TrimSpace(b))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n")), breaking
}

// Changes is this release's changelog between two versions.
func Changes(from, to string) (string, []string) { return Between(changelog, from, to) }

// Migration takes workspace.toml's text from one schema to the next.
type Migration func(text string) (string, error)

// Migrations are keyed by the schema they migrate from. A release that changes the configuration
// schema adds one here.
var Migrations = map[int]Migration{}

var schemaLine = regexp.MustCompile(`(?m)^schema\s*=\s*(\d+)\s*$`)

// Migrate brings workspace.toml's text to the schema to, one step at a time.
func Migrate(text string, to int) (string, error) {
	m := schemaLine.FindStringSubmatch(text)
	if m == nil {
		return "", fmt.Errorf("workspace.toml has no schema line → add schema = %d under [workspace]", to)
	}
	from, _ := strconv.Atoi(m[1])
	for v := from; v < to; v++ {
		step, ok := Migrations[v]
		if !ok {
			return "", fmt.Errorf("this aw cannot migrate configuration schema %d → upgrade through a release that can", v)
		}
		next, err := step(text)
		if err != nil {
			return "", fmt.Errorf("migrating configuration schema %d to %d: %w", v, v+1, err)
		}
		text = schemaLine.ReplaceAllString(next, fmt.Sprintf("schema = %d", v+1))
	}
	return text, nil
}

// Hash is a managed file's recorded hash.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Recorded returns the hash recorded for a managed path, or "".
func Recorded(owner, path string) string {
	data, _ := os.ReadFile(filepath.Join(owner, ManagedPath))
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == path {
			return f[0]
		}
	}
	return ""
}

// Record sets a managed path's hash. An empty hash removes the entry, which ejects the file.
func Record(owner, path, hash string) error {
	name := filepath.Join(owner, ManagedPath)
	data, err := os.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var kept []string
	done := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if f := strings.Fields(line); len(f) == 2 && f[1] == path {
			if hash != "" {
				kept = append(kept, hash+"  "+path)
			}
			done = true
			continue
		}
		kept = append(kept, line)
	}
	if !done && hash != "" {
		kept = append(kept, hash+"  "+path)
	}
	return os.WriteFile(name, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}
