// Package plans is the plan mechanism (R82-R84): a status convention a reader with no session
// memory can resume from, an index, a checker, and promotion from individual to team. It ships no
// plan content.
package plans

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Fields are the Status block's fields, and States the values of State.
var (
	Fields = []string{"State", "Updated", "Branches / MRs", "Done", "In progress", "Next step"}
	States = []string{"DRAFT", "PLANNED", "PARTIALLY IMPLEMENTED", "FULLY IMPLEMENTED", "FULLY VERIFIED"}
)

// Status is a plan's parsed Status block.
type Status struct {
	Slug   string            `json:"slug"`
	Path   string            `json:"path"`
	Fields map[string]string `json:"fields"`
}

var (
	statusBlock = regexp.MustCompile(`(?ms)^## Status\s*$(.*?)(?:^## |\z)`)
	fieldLine   = regexp.MustCompile(`\*\*([^:*]+):\*\*\s*(.*)`)
	link        = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
)

// Parse reads a plan's Status block, or reports that it has none.
func Parse(path string) (*Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := statusBlock.FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("no '## Status' section")
	}
	s := &Status{Slug: strings.TrimSuffix(filepath.Base(path), ".md"), Path: path, Fields: map[string]string{}}
	for _, f := range fieldLine.FindAllStringSubmatch(string(m[1]), -1) {
		if _, seen := s.Fields[f[1]]; !seen {
			s.Fields[f[1]] = strings.TrimSpace(f[2])
		}
	}
	return s, nil
}

// List returns the plans in a directory.
func List(dir string) ([]*Status, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []*Status
	for _, f := range files {
		if filepath.Base(f) == "README.md" {
			continue
		}
		if s, err := Parse(f); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// Template is a new plan.
func Template(title, date string) string {
	return fmt.Sprintf(`# %s

## Status

**State:** DRAFT
**Updated:** %s
**Branches / MRs:** none yet
**Done:** nothing yet
**In progress:** writing the plan
**Next step:** finish this plan

## Goal
`, title, date)
}

var skipped = []string{".git", ".worktrees", ".lanes", "node_modules", "vendor"}

// Check verifies the plan convention in root's plans/ and the relative Markdown links under paths
// (all of root when there are none). It returns the problems it finds; a workspace with no plans/
// has none of the first kind.
func Check(root string, paths ...string) ([]string, error) {
	var problems []string
	dir := filepath.Join(root, "plans")
	index, indexErr := os.ReadFile(filepath.Join(dir, "README.md"))
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, f := range files {
		base := filepath.Base(f)
		if base == "README.md" {
			continue
		}
		s, err := Parse(f)
		if err != nil {
			problems = append(problems, "plans/"+base+": "+err.Error())
			continue
		}
		for _, name := range Fields {
			if _, ok := s.Fields[name]; !ok {
				problems = append(problems, fmt.Sprintf("plans/%s: Status block is missing **%s:**", base, name))
			}
		}
		state := s.Fields["State"]
		if state != "" && !slices.Contains(States, state) {
			problems = append(problems, fmt.Sprintf("plans/%s: State is %q, which is not one of %s", base, state, strings.Join(States, ", ")))
		}
		if indexErr == nil {
			row := indexRow(string(index), base)
			switch {
			case row == "":
				problems = append(problems, "plans/README.md: no row for "+base)
			case state != "" && !strings.Contains(row, "| "+state+" |"):
				problems = append(problems, fmt.Sprintf("plans/README.md: the row for %s does not say %s", base, state))
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var err error
	for _, start := range paths {
		if err = checkLinks(root, filepath.Join(root, start), &problems); err != nil {
			break
		}
	}
	return problems, err
}

func checkLinks(root, start string, problems *[]string) error {
	return filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains(skipped, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range link.FindAllStringSubmatch(stripCode(string(data)), -1) {
			target, _, _ := strings.Cut(m[1], "#")
			if target == "" || strings.Contains(m[1], "://") || strings.HasPrefix(m[1], "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), target)); err != nil {
				*problems = append(*problems, fmt.Sprintf("%s: broken link %s", rel, m[1]))
			}
		}
		return nil
	})
}

func indexRow(index, base string) string {
	for _, line := range strings.Split(index, "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, "("+base+")") {
			return line
		}
	}
	return ""
}

var fenced = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

func stripCode(s string) string { return fenced.ReplaceAllString(s, "") }
