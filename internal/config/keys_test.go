package config

import (
	"slices"
	"testing"
)

func TestKeysEnding(t *testing.T) {
	if got := KeysEnding(Path{"accepted_findings"}); !slices.Equal(got, []string{"projects.<name>.accepted_findings"}) {
		t.Errorf("accepted_findings: %q", got)
	}
	if got := KeysEnding(Path{"local_compose"}); !slices.Equal(got, []string{"projects.<name>.local_compose"}) {
		t.Errorf("local_compose: %q", got)
	}
	if got := KeysEnding(Path{"nonsense"}); len(got) != 0 {
		t.Errorf("nonsense: %q", got)
	}
	if got := KeysEnding(Path{"after"}); !slices.Contains(got, "projects.<name>.servers.<name>.after") {
		t.Errorf("after: %q", got)
	}
}
