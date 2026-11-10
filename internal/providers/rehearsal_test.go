package providers

import (
	"slices"
	"testing"
)

// A rehearsal looks only in its own Compose project: the owner's database is a service of the same name.
func TestContainerQueryStaysInTheRehearsal(t *testing.T) {
	const own = "label=com.docker.compose.project=rhab-infra"
	if q := containerQuery("label=com.docker.compose.service=db", "rhab-infra"); !slices.Contains(q, own) {
		t.Errorf("a rehearsal's query is not limited to its project: %v", q)
	}
	if q := containerQuery("label=com.docker.compose.service=db", ""); slices.Contains(q, own) {
		t.Errorf("an ordinary query is limited: %v", q)
	}
}
