package scenario

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Requirement is one entry of requirements.toml.
type Requirement struct {
	// Milestone is the first milestone named for the requirement in the plan's §2. From then on it
	// must be covered.
	Milestone string `toml:"milestone"`
	// CoveredBy is "scenario" (the default), or "bench", "ci" or "docs" for requirements that a scenario
	// cannot show, such as a latency budget or the CI platform matrix.
	CoveredBy string `toml:"covered_by"`
}

// Requirements is acceptance/requirements.toml.
type Requirements struct {
	// Current is the latest milestone whose scenarios must pass. Scenarios from later milestones
	// are pending. It moves forward when a milestone's scenarios are all written and passing.
	Current string `toml:"current"`
	// InProgress is the milestone being built. Its written scenarios run and must pass; those still
	// holding only the placeholder unwritten stay pending. It is empty or the milestone after Current.
	InProgress string                 `toml:"in_progress"`
	Req        map[string]Requirement `toml:"req"`
}

// LoadRequirements reads and checks requirements.toml strictly.
func LoadRequirements(path string) (*Requirements, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Requirements
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := ParseMilestone(r.Current); err != nil {
		return nil, fmt.Errorf("%s: current: %v", path, err)
	}
	if r.InProgress != "" {
		cur, _ := ParseMilestone(r.Current)
		next, err := ParseMilestone(r.InProgress)
		if err != nil || next != cur+1 {
			return nil, fmt.Errorf("%s: in_progress must be empty or the milestone after current (%s)", path, r.Current)
		}
	}
	for key, req := range r.Req {
		if !reqRE.MatchString(key) {
			return nil, fmt.Errorf("%s: [req.%s]: not a requirement key", path, key)
		}
		if _, err := ParseMilestone(req.Milestone); err != nil {
			return nil, fmt.Errorf("%s: [req.%s] milestone: %v", path, key, err)
		}
		switch req.CoveredBy {
		case "", "scenario", "bench", "ci", "docs":
		default:
			return nil, fmt.Errorf("%s: [req.%s] covered_by: %q is not scenario, bench, ci or docs", path, key, req.CoveredBy)
		}
	}
	return &r, nil
}

// CurrentMilestone returns r.Current, or the AW_ACCEPT_MILESTONE override, which runs scenarios of
// a later milestone while they are written.
func (r *Requirements) CurrentMilestone() (Milestone, error) {
	if v := os.Getenv("AW_ACCEPT_MILESTONE"); v != "" {
		return ParseMilestone(v)
	}
	return ParseMilestone(r.Current)
}

// Number returns the requirement number of a key such as "R12".
func Number(key string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(key, "R"))
	return n
}

// InProgressMilestone returns the milestone being built, or -1.
func (r *Requirements) InProgressMilestone() Milestone {
	m, err := ParseMilestone(r.InProgress)
	if err != nil {
		return -1
	}
	return m
}
