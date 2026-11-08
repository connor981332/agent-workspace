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
