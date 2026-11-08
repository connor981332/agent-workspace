// Package coverage is the coverage check (plans/release-candidate.md §3.6). It keeps
// requirements.toml, the spec's requirement list, the plan's §2 table and the scenarios' "# req:"
// tags in agreement, and fails when a requirement whose milestone has been reached has no scenario
// that runs.
package coverage_test

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/connor981332/agent-workspace/acceptance/scenario"
)

const (
	requirementsToml = "../requirements.toml"
	requirementsSpec = "../../docs/spec/requirements.md"
	plan             = "../../plans/release-candidate.md"
	scenarios        = "../scenarios"
)

func load(t *testing.T) (*scenario.Requirements, []scenario.Scenario, scenario.Milestone) {
	t.Helper()
	reqs, err := scenario.LoadRequirements(requirementsToml)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reqs.CurrentMilestone()
	if err != nil {
		t.Fatal(err)
	}
	all, err := scenario.Find(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	return reqs, all, current
}

func specRequirements(t *testing.T) []int {
	t.Helper()
	data, err := os.ReadFile(requirementsSpec)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, m := range regexp.MustCompile(`(?m)^- \*\*R([0-9]+)\.\*\*`).FindAllStringSubmatch(string(data), -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatalf("found no requirements in %s", requirementsSpec)
	}
	return out
}

func TestTomlListsExactlyTheSpecRequirements(t *testing.T) {
	reqs, _, _ := load(t)
	spec := specRequirements(t)
	for _, n := range spec {
		if _, ok := reqs.Req[fmt.Sprintf("R%d", n)]; !ok {
			t.Errorf("R%d is in %s but not in requirements.toml", n, requirementsSpec)
		}
	}
	for key := range reqs.Req {
		if !slices.Contains(spec, scenario.Number(key)) {
			t.Errorf("%s is in requirements.toml but not in %s", key, requirementsSpec)
		}
	}
}

func TestTomlMatchesThePlan(t *testing.T) {
	reqs, _, _ := load(t)
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`(?m)^\| R([0-9]+) \| [^|]* \| ([^|]*) \|`).FindAllStringSubmatch(string(data), -1)
	if len(rows) == 0 {
		t.Fatalf("found no §2 rows in %s", plan)
	}
	first := regexp.MustCompile(`M[0-9]+`)
	for _, row := range rows {
		key := "R" + row[1]
		want := first.FindString(row[2])
		got, ok := reqs.Req[key]
		switch {
		case !ok:
			t.Errorf("%s is in the plan's §2 but not in requirements.toml", key)
		case got.Milestone != want:
			t.Errorf("%s: requirements.toml says %s, the plan's §2 says %s (%q)", key, got.Milestone, want, row[2])
		}
	}
}

func TestScenariosCiteRealRequirements(t *testing.T) {
	reqs, all, _ := load(t)
	for _, sc := range all {
		for _, n := range sc.Req {
			if _, ok := reqs.Req[fmt.Sprintf("R%d", n)]; !ok {
				t.Errorf("%s cites R%d, which does not exist", sc.Name, n)
			}
		}
	}
}

func TestReachedRequirementsAreCovered(t *testing.T) {
	reqs, all, current := load(t)
	for key, req := range reqs.Req {
		m, _ := scenario.ParseMilestone(req.Milestone)
		if m > current || (req.CoveredBy != "" && req.CoveredBy != "scenario") {
			continue
		}
		n := scenario.Number(key)
		covered := slices.ContainsFunc(all, func(sc scenario.Scenario) bool {
			return sc.Milestone <= current && slices.Contains(sc.Req, n)
		})
		if !covered {
			t.Errorf("%s is due from %s (current %s), but no running scenario cites it", key, req.Milestone, current)
		}
	}
}

// TestSafetyModelIsStated covers R94, whose subject is a document: it must say plainly what the
// guards are not, and name what is left unprotected.
func TestSafetyModelIsStated(t *testing.T) {
	data, err := os.ReadFile("../../docs/safety.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cooperative agents", "not a security sandbox", "not confined", "fail closed", "network is not restricted"} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).Match(data) {
			t.Errorf("docs/safety.md does not say %q", want)
		}
	}
}
