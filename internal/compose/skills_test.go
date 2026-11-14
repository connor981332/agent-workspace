package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestShippedSkills keeps skills/shipped.txt complete: a changed skill must list its new hash, so
// that the release after it can tell the shipped version from a hand edit.
func TestShippedSkills(t *testing.T) {
	for _, name := range Skills {
		data, err := skillFiles.ReadFile("skills/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if h := hashOf(data); !shipped(name, h) {
			t.Errorf("internal/compose/skills/shipped.txt does not list %s's current hash → add the line \"%s  %s\"", name, h, name)
		}
	}
}

// A skill file that holds a version aw shipped is replaced, even when .aw/managed lost its hash,
// and a file that is up to date gets its missing hash back. A real edit is still left alone.
func TestApplySkillsTrustsShippedVersions(t *testing.T) {
	owner := t.TempDir()
	old := ".claude/skills/aw-readiness-audit/SKILL.md"
	upToDate := ".agents/skills/aw-workspace-design/SKILL.md"
	edited := ".opencode/commands/aw-readiness-audit.md"
	// The list holds only the hashes of old versions, so stand in for one.
	oldContent := []byte("an older readiness-audit\n")
	saved := shippedSkills
	shippedSkills += hashOf(oldContent) + "  readiness-audit\n"
	t.Cleanup(func() { shippedSkills = saved })
	if err := os.MkdirAll(filepath.Join(owner, ".aw"), 0o755); err != nil {
		t.Fatal(err)
	}
	current, _ := skillFiles.ReadFile("skills/workspace-design.md")
	for path, body := range map[string][]byte{old: oldContent, upToDate: current, edited: []byte("my own notes\n")} {
		p := filepath.Join(owner, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	changed, editedList, err := ApplySkills(owner)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changed, old) {
		t.Errorf("the shipped old version was not replaced: changed %v", changed)
	}
	if !slices.Equal(editedList, []string{edited}) {
		t.Errorf("edited = %v, want only %s", editedList, edited)
	}
	if recordedHash(owner, upToDate) != hashOf(current) {
		t.Errorf("the up-to-date file's hash was not recorded")
	}
}
