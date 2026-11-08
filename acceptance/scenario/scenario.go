// Package scenario reads the headers of acceptance scenarios and compares milestones. It is shared
// by the runner and the coverage check.
package scenario

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Milestone is a plan milestone, M0 to M12.
type Milestone int

// LastMilestone is M12, the 1.0 release.
const LastMilestone Milestone = 12

// ParseMilestone parses "M3".
func ParseMilestone(s string) (Milestone, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "M"))
	if !strings.HasPrefix(strings.TrimSpace(s), "M") || err != nil || n < 0 || Milestone(n) > LastMilestone {
		return 0, fmt.Errorf("%q is not a milestone (M0 to M%d)", s, LastMilestone)
	}
	return Milestone(n), nil
}

func (m Milestone) String() string { return "M" + strconv.Itoa(int(m)) }

// Scenario is one .txtar file under scenarios/.
type Scenario struct {
	Path      string    // absolute path
	Name      string    // <area>/<name>, without .txtar
	Milestone Milestone // from which it must pass
	Req       []int     // requirement numbers from "# req:"
	Source    string    // "# source:", if any
	Unwritten bool      // the script is only the placeholder unwritten
}

var reqRE = regexp.MustCompile(`^R([1-9][0-9]*)$`)

// Parse reads a scenario's header: the comment lines before the first line of the script.
func Parse(path, name string) (Scenario, error) {
	sc := Scenario{Path: path, Name: name}
	f, err := os.Open(path)
	if err != nil {
		return sc, err
	}
	defer f.Close()

	var haveMilestone, inScript bool
	var script []string
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		if strings.HasPrefix(text, "-- ") && strings.HasSuffix(text, " --") {
			break // the archive's files start
		}
		if text == "" {
			continue
		}
		if inScript || !strings.HasPrefix(text, "#") {
			inScript = true
			if !strings.HasPrefix(text, "#") {
				script = append(script, text)
			}
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, "#")), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "milestone":
			if haveMilestone {
				return sc, fmt.Errorf("%s:%d: a second milestone line", path, line)
			}
			m, err := ParseMilestone(value)
			if err != nil {
				return sc, fmt.Errorf("%s:%d: %v", path, line, err)
			}
			sc.Milestone, haveMilestone = m, true
		case "req":
			for _, field := range strings.Fields(strings.ReplaceAll(value, ",", " ")) {
				m := reqRE.FindStringSubmatch(field)
				if m == nil {
					return sc, fmt.Errorf("%s:%d: %q is not a requirement (R1, R2, …)", path, line, field)
				}
				n, _ := strconv.Atoi(m[1])
				sc.Req = append(sc.Req, n)
			}
		case "source":
			sc.Source = value
		}
	}
	if err := s.Err(); err != nil {
		return sc, err
	}
	sc.Unwritten = len(script) == 1 && script[0] == "unwritten"
	if !haveMilestone {
		return sc, fmt.Errorf("%s: no '# milestone: Mn' line in the header", path)
	}
	return sc, nil
}

// Find returns every scenario under dir (scenarios/<area>/<name>.txtar), sorted by name.
func Find(dir string) ([]Scenario, error) {
	var out []Scenario
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txtar") {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(strings.TrimSuffix(rel, ".txtar"))
		if strings.Count(name, "/") != 1 {
			return fmt.Errorf("%s: scenarios live at scenarios/<area>/<name>.txtar", path)
		}
		sc, err := Parse(path, name)
		if err != nil {
			return err
		}
		out = append(out, sc)
		return nil
	})
	slices.SortFunc(out, func(a, b Scenario) int { return strings.Compare(a.Name, b.Name) })
	return out, err
}
