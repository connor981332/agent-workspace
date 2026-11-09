package upgrade

import (
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	order := []string{"0.0.9", "0.0.10", "0.1.0-alpha", "0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.10", "0.1.0", "0.2.0", "1.0.0"}
	for i := range order {
		for j := range order {
			got := Compare(order[i], order[j])
			if (i < j) != (got < 0) || (i > j) != (got > 0) {
				t.Errorf("Compare(%s, %s) = %d", order[i], order[j], got)
			}
		}
	}
}

func TestBetween(t *testing.T) {
	text := "# Changelog\n\n## 0.3.0\n\n- three\n- **Breaking:** renamed x\n\n## 0.2.0\n\n- two\n\n## 0.1.0\n\n- one\n"
	notes, breaking := Between(text, "0.1.0", "0.2.0")
	if !strings.Contains(notes, "two") || strings.Contains(notes, "three") || strings.Contains(notes, "- one") || len(breaking) != 0 {
		t.Fatalf("notes %q, breaking %v", notes, breaking)
	}
	if _, breaking = Between(text, "0.1.0", "0.3.0"); len(breaking) != 1 || breaking[0] != "renamed x" {
		t.Fatalf("breaking %v", breaking)
	}
}

func TestMigrateStepsThroughSchemas(t *testing.T) {
	defer func() { Migrations = map[int]Migration{} }()
	Migrations = map[int]Migration{
		1: func(s string) (string, error) { return strings.ReplaceAll(s, "old", "mid"), nil },
		2: func(s string) (string, error) { return strings.ReplaceAll(s, "mid", "new"), nil },
	}
	got, err := Migrate("[workspace]\nschema = 1\nkey = \"old\"\n", 3)
	if err != nil || !strings.Contains(got, "schema = 3") || !strings.Contains(got, `"new"`) {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := Migrate("schema = 9\n", 10); err == nil {
		t.Fatal("no migration from 9: want an error")
	}
}

func TestShippedChangelogParses(t *testing.T) {
	if notes, _ := Changes("0.1.0", "0.2.0"); !strings.Contains(notes, "aw upgrade") {
		t.Fatalf("notes %q", notes)
	}
}
