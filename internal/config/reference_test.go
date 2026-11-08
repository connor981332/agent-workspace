package config

import (
	"os"
	"testing"
)

const referencePath = "../../docs/reference/config.md"

// TestReference keeps docs/reference/config.md generated from the schema.
func TestReference(t *testing.T) {
	want := Reference()
	if os.Getenv("AW_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("../../docs/reference", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(referencePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(referencePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s is out of date with the schema → run AW_UPDATE_GOLDEN=1 go test ./internal/config", referencePath)
	}
}
