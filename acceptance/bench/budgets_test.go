package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestBudgetsMatchTheSpec keeps budgets.toml in step with the budget table in speed.md, so a budget
// cannot drift from the spec unnoticed (§5.1).
func TestBudgetsMatchTheSpec(t *testing.T) {
	ops, err := loadBudgets("budgets.toml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../docs/spec/speed.md")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(data)), " ")
	spec := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^\| (.+?) \| (\d+) ms`).FindAllStringSubmatch(string(data), -1) {
		n, _ := strconv.Atoi(m[2])
		spec[strings.TrimSpace(m[1])] = n
	}
	if len(spec) < 8 {
		t.Fatalf("read only %d budgets from speed.md", len(spec))
	}
	covered := map[string]bool{}
	for _, op := range ops {
		want, ok := spec[op.Spec]
		switch {
		case !ok:
			t.Errorf("%s: speed.md has no budget row %q", op.Name, op.Spec)
		case op.BudgetMS != want:
			t.Errorf("%s: budgets.toml says %d ms, speed.md says %d ms", op.Name, op.BudgetMS, want)
		}
		// A CI ceiling must be stated in speed.md's "Enforcement" too.
		if op.CIBudgetMS > 0 && !strings.Contains(flat, fmt.Sprintf("above %d ms p99", op.CIBudgetMS)) {
			t.Errorf("%s: budgets.toml sets a CI ceiling of %d ms, which speed.md does not state", op.Name, op.CIBudgetMS)
		}
		covered[op.Spec] = true
	}
	for row := range spec {
		if !covered[row] {
			t.Errorf("speed.md's budget %q has no operation in budgets.toml", row)
		}
	}
}

func TestPercentile(t *testing.T) {
	var s []float64
	for i := 1; i <= 100; i++ {
		s = append(s, float64(i))
	}
	if p := percentile(s, 99); p != 99 {
		t.Errorf("p99 = %v", p)
	}
	if p := percentile(s, 50); p != 50 {
		t.Errorf("p50 = %v", p)
	}
	if p := percentile([]float64{7}, 99); p != 7 {
		t.Errorf("p99 of one = %v", p)
	}
}
