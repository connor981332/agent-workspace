package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is the state schema this binary reads and writes: the number of migrations.
var SchemaVersion = len(migrations)

// migrations are applied in order. Migration i (from 0) takes the schema from version i to i+1.
// A released migration is never edited: a change is a new migration.
var migrations = []string{
	// 1: the meta table, which records who created the state and when.
	`CREATE TABLE meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	) STRICT`,

	// 2: lanes, their repositories, and sessions (M2).
	`CREATE TABLE lanes (
		number     INTEGER PRIMARY KEY CHECK (number > 0),
		name       TEXT UNIQUE,
		purpose    TEXT NOT NULL DEFAULT '',
		state      TEXT NOT NULL,
		generation INTEGER NOT NULL DEFAULT 0,
		created    TEXT NOT NULL,
		claimed    TEXT,
		updated    TEXT NOT NULL,
		pooled     TEXT
	) STRICT;
	CREATE TABLE lane_repos (
		lane     INTEGER NOT NULL REFERENCES lanes (number) ON DELETE CASCADE,
		project  TEXT NOT NULL,
		path     TEXT NOT NULL,
		branch   TEXT NOT NULL,
		base_ref TEXT NOT NULL,
		base_sha TEXT NOT NULL,
		PRIMARY KEY (lane, project)
	) STRICT;
	CREATE TABLE sessions (
		id              INTEGER PRIMARY KEY,
		harness         TEXT NOT NULL,
		harness_session TEXT NOT NULL,
		lane            INTEGER REFERENCES lanes (number) ON DELETE SET NULL,
		lane_generation INTEGER,
		purpose         TEXT NOT NULL DEFAULT '',
		state           TEXT NOT NULL,
		started         TEXT NOT NULL,
		last_activity   TEXT NOT NULL,
		pid             INTEGER,
		pid_start       TEXT,
		UNIQUE (harness, harness_session)
	) STRICT;
	CREATE TABLE journal (
		id      INTEGER PRIMARY KEY,
		kind    TEXT NOT NULL,
		lane    INTEGER,
		step    TEXT NOT NULL,
		status  TEXT NOT NULL,
		detail  TEXT NOT NULL DEFAULT '',
		started TEXT NOT NULL,
		ended   TEXT
	) STRICT`,

	// 3: handoffs, written by aw finish (M2).
	`CREATE TABLE handoffs (
		id         INTEGER PRIMARY KEY,
		lane       INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		created    TEXT NOT NULL,
		summary    TEXT NOT NULL,
		plan       TEXT NOT NULL DEFAULT '',
		heads      TEXT NOT NULL
	) STRICT;
	CREATE INDEX handoffs_lane ON handoffs (lane, generation)`,

	// 4: the processes that joined a session after it started (a resumed harness keeps its session
	// ID in a new process). The lane is held until the last of them exits.
	`CREATE TABLE session_processes (
		session   INTEGER NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
		pid       INTEGER NOT NULL,
		pid_start TEXT NOT NULL,
		PRIMARY KEY (session, pid, pid_start)
	) STRICT`,

	// 5: pair sessions (R60), and the ID aw issued to a session whose harness later told it its own,
	// which the session's commands still carry in AW_SESSION.
	`ALTER TABLE sessions ADD COLUMN pair INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE sessions ADD COLUMN issued_session TEXT NOT NULL DEFAULT ''`,

	// 6: when a handoff was picked up by aw open. It stays on record, but no longer describes the lane.
	`ALTER TABLE handoffs ADD COLUMN archived TEXT NOT NULL DEFAULT ''`,

	// 7: resources (M4): seats held or reserved, the processes behind them, the queue, and
	// notifications waiting for an agent's next tool call. Lane 0 is the owner.
	`CREATE TABLE leases (
		id            INTEGER PRIMARY KEY,
		resource      TEXT NOT NULL,
		lane          INTEGER NOT NULL,
		generation    INTEGER NOT NULL DEFAULT 0,
		lane_name     TEXT NOT NULL DEFAULT '',
		seat          INTEGER NOT NULL,
		state         TEXT NOT NULL,
		token         TEXT NOT NULL DEFAULT '',
		granted       TEXT NOT NULL,
		claim_by      TEXT NOT NULL DEFAULT '',
		last_activity TEXT NOT NULL,
		pid           INTEGER NOT NULL DEFAULT 0,
		pid_start     TEXT NOT NULL DEFAULT '',
		UNIQUE (resource, lane),
		UNIQUE (resource, seat)
	) STRICT;
	CREATE TABLE lease_procs (
		lease     INTEGER NOT NULL REFERENCES leases (id) ON DELETE CASCADE,
		server    TEXT NOT NULL,
		pid       INTEGER NOT NULL,
		pid_start TEXT NOT NULL,
		log       TEXT NOT NULL,
		PRIMARY KEY (lease, server)
	) STRICT;
	CREATE TABLE queue (
		id         INTEGER PRIMARY KEY,
		request    TEXT NOT NULL,
		resource   TEXT NOT NULL,
		lane       INTEGER NOT NULL,
		generation INTEGER NOT NULL DEFAULT 0,
		lane_name  TEXT NOT NULL DEFAULT '',
		priority   INTEGER NOT NULL,
		enqueued   TEXT NOT NULL,
		UNIQUE (resource, lane)
	) STRICT;
	CREATE TABLE notifications (
		id        INTEGER PRIMARY KEY,
		lane      INTEGER NOT NULL,
		kind      TEXT NOT NULL,
		message   TEXT NOT NULL,
		created   TEXT NOT NULL,
		delivered TEXT NOT NULL DEFAULT ''
	) STRICT;
	CREATE INDEX notifications_pending ON notifications (lane, delivered)`,

	// 8: when a holder over its maximum hold was warned.
	`ALTER TABLE leases ADD COLUMN warned TEXT NOT NULL DEFAULT ''`,

	// 9: the lifecycle journal (M5): a lane's setup steps, and the process running them.
	`CREATE TABLE lane_setups (
		lane       INTEGER PRIMARY KEY,
		generation INTEGER NOT NULL,
		from_pool  INTEGER NOT NULL DEFAULT 0,
		pid        INTEGER NOT NULL DEFAULT 0,
		pid_start  TEXT NOT NULL DEFAULT '',
		created    TEXT NOT NULL
	) STRICT;
	CREATE TABLE lane_steps (
		lane        INTEGER NOT NULL,
		generation  INTEGER NOT NULL,
		step        TEXT NOT NULL,
		seq         INTEGER NOT NULL,
		status      TEXT NOT NULL,
		detail      TEXT NOT NULL DEFAULT '',
		log         TEXT NOT NULL DEFAULT '',
		exit_status INTEGER,
		started     TEXT NOT NULL DEFAULT '',
		ended       TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (lane, generation, step)
	) STRICT`,

	// 10: what providers made for each lane, which is all they may act on, and named snapshots.
	`CREATE TABLE lane_data (
		lane   INTEGER NOT NULL,
		data   TEXT NOT NULL,
		object TEXT NOT NULL,
		PRIMARY KEY (lane, data, object)
	) STRICT;
	CREATE TABLE snapshots (
		data    TEXT NOT NULL,
		name    TEXT NOT NULL,
		lane    INTEGER NOT NULL,
		objects TEXT NOT NULL,
		created TEXT NOT NULL,
		PRIMARY KEY (data, name)
	) STRICT`,

	// 11: a lane's role, and the human's delegation (M9): at most one at a time.
	`ALTER TABLE lanes ADD COLUMN role TEXT NOT NULL DEFAULT '';
	CREATE TABLE delegations (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		scope   TEXT NOT NULL,
		granted TEXT NOT NULL,
		expires TEXT NOT NULL
	) STRICT`,

	// 12: the name a lane's branch was pushed under, when the team's branch names differ from aw's.
	`ALTER TABLE lane_repos ADD COLUMN published TEXT NOT NULL DEFAULT ''`,
}

// BackupPath is where the database is copied before migrating from schema version v.
func BackupPath(path string, v int) string { return fmt.Sprintf("%s.bak-%d", path, v) }

// migrate brings the database to SchemaVersion. An existing database (version > 0) is first copied
// to state.db.bak-<version>, with VACUUM INTO, which gives a consistent copy even under WAL.
func (s *Store) migrate() error {
	if s.version == SchemaVersion {
		return nil
	}
	if s.version > 0 {
		backup := BackupPath(s.path, s.version)
		if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("replacing the old backup %s: %w", backup, err)
		}
		if _, err := s.db.Exec(`VACUUM INTO ?`, backup); err != nil {
			return fmt.Errorf("backing up %s before migrating it: %w → free some disk space, then retry; nothing was changed", s.path, err)
		}
		if err := os.Chmod(backup, 0o600); err != nil {
			return err
		}
	}
	from := s.version
	if from == 0 {
		var mode string
		if err := s.db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil || mode != "wal" {
			return fmt.Errorf("putting %s in WAL mode: %v (mode %q) → check that the file system supports it", s.path, err, mode)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for v := from; v < SchemaVersion; v++ {
		if _, err := tx.Exec(migrations[v]); err != nil {
			return fmt.Errorf("migrating %s from state schema %d to %d: %w → nothing was changed; report this with the output of aw doctor", s.path, v, v+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion)); err != nil {
		return err
	}
	if from == 0 {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('created', ?)`, now()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('migrated', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		fmt.Sprintf("%d→%d %s", from, SchemaVersion, now())); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.version = SchemaVersion
	return nil
}

// Backups lists the backups next to the database.
func Backups(dir string) ([]string, error) {
	return filepath.Glob(filepath.Join(dir, File+".bak-*"))
}
