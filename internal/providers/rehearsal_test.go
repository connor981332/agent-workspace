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

func TestPostgresStarting(t *testing.T) {
	for msg, want := range map[string]bool{
		`psql: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed: FATAL: the database system is shutting down`: true,
		`psql: FATAL: the database system is starting up`:                                                            true,
		`psql: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed: No such file or directory`: true,
		`psql: FATAL: password authentication failed for user "postgres"`:                                            false,
		`psql: ERROR: database "x" does not exist`:                                                                   false,
	} {
		if got := postgresStarting(msg); got != want {
			t.Errorf("%s: got %v", msg, got)
		}
	}
}
