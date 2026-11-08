package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCreateAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aw")
	if _, err := Open(dir, ReadOnly); !errors.Is(err, ErrNoState) {
		t.Fatalf("read-only open of a missing store: %v", err)
	}
	s, err := Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != SchemaVersion || s.ReadOnly() != nil {
		t.Fatalf("version %d, read-only %v", s.Version(), s.ReadOnly())
	}
	if created, _ := s.Meta("created"); created == "" {
		t.Errorf("no created time recorded")
	}
	if err := s.SetMeta(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %q, %v", mode, err)
	}
	s.Close()

	for _, p := range []string{dir, filepath.Join(dir, File)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s has mode %v; state must be private", p, perm)
		}
	}
	if backups, _ := Backups(dir); len(backups) != 0 {
		t.Errorf("a new database was backed up: %v", backups)
	}

	r, err := Open(dir, ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, _ := r.Meta("k"); v != "v" {
		t.Errorf("read back %q", v)
	}
	var ro *ReadOnlyError
	if err := r.SetMeta(context.Background(), "k", "w"); !errors.As(err, &ro) {
		t.Errorf("a read-only store accepted a write: %v", err)
	}
}

func setVersion(t *testing.T, dir string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA user_version = ` + strconv.Itoa(v)); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchemaIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	setVersion(t, dir, SchemaVersion+1)

	s, err = Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ro := s.ReadOnly()
	if ro == nil {
		t.Fatal("a newer schema was opened for writing")
	}
	err = s.SetMeta(context.Background(), "k", "v")
	if !errors.As(err, &ro) || s.Version() != SchemaVersion+1 {
		t.Fatalf("write to a newer schema: %v", err)
	}
	if want := "newer than this aw's"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "bin/aw") {
		t.Errorf("message %q does not explain and name the fix", err)
	}
	// Nothing was migrated or backed up.
	if backups, _ := Backups(dir); len(backups) != 0 {
		t.Errorf("backups %v", backups)
	}
}

func TestUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte("this is not a database, but it is long enough to look like one"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{ReadOnly, ReadWrite} {
		_, err := Open(dir, mode)
		var u *UnreadableError
		if !errors.As(err, &u) || !strings.Contains(err.Error(), "treated as protected") {
			t.Errorf("mode %d: %v", mode, err)
		}
	}
	// It was not overwritten.
	if data, _ := os.ReadFile(filepath.Join(dir, File)); !strings.Contains(string(data), "not a database") {
		t.Errorf("the unreadable file was changed")
	}
}

func TestMigrationBacksUpFirst(t *testing.T) {
	old := migrations
	t.Cleanup(func() { migrations, SchemaVersion = old, len(old) })

	dir := t.TempDir()
	s, err := Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(context.Background(), "before", "yes"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	migrations = append(append([]string{}, old...), `CREATE TABLE extra (x INTEGER) STRICT`)
	SchemaVersion = len(migrations)
	s, err = Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != len(old)+1 {
		t.Errorf("version %d", s.Version())
	}
	s.Close()
	backup := BackupPath(filepath.Join(dir, File), len(old))
	b, err := sql.Open("sqlite", "file:"+backup+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var v int
	var before string
	if err := b.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(old) {
		t.Errorf("backup version %d, %v", v, err)
	}
	if err := b.QueryRow(`SELECT value FROM meta WHERE key = 'before'`).Scan(&before); err != nil || before != "yes" {
		t.Errorf("backup content: %q, %v", before, err)
	}

	// A failing migration changes nothing.
	migrations = append(migrations, `THIS IS NOT SQL`)
	SchemaVersion = len(migrations)
	if _, err := Open(dir, ReadWrite); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("a failing migration: %v", err)
	}
	migrations = migrations[:len(migrations)-1]
	SchemaVersion = len(migrations)
	s, err = Open(dir, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != len(old)+1 {
		t.Errorf("version after a failed migration: %d", s.Version())
	}
}

func TestEveryTableExists(t *testing.T) {
	s, err := Open(t.TempDir(), ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"meta", "lanes", "lane_repos", "sessions", "journal", "handoffs"} {
		var n int
		if err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: %d, %v", table, n, err)
		}
	}
}

// An archived handoff stays on record, marked, so aw open can give it to another session.
func TestArchivedHandoffStaysOnRecord(t *testing.T) {
	s, err := Open(t.TempDir(), ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if err := InsertLane(tx, Lane{Number: 1, State: LaneReady, Generation: 1, Created: now(), Updated: now()}); err != nil {
			return err
		}
		return AddHandoff(tx, Handoff{Lane: 1, Generation: 1, Summary: "done", Heads: "{}"})
	}); err != nil {
		t.Fatal(err)
	}
	h, err := LatestHandoff(s.DB(), 1, 1)
	if err != nil || h.Archived != "" {
		t.Fatalf("before: %+v, %v", h, err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return ArchiveHandoff(tx, h.ID) }); err != nil {
		t.Fatal(err)
	}
	if h, err = LatestHandoff(s.DB(), 1, 1); err != nil || h.Archived == "" || h.Summary != "done" {
		t.Fatalf("after: %+v, %v", h, err)
	}
}

// The log is kept between commands, so it must not grow without limit, and what each command wrote
// must be there for the next.
func TestKeptLogStaysBoundedAndLosesNothing(t *testing.T) {
	dir := t.TempDir()
	const commands = 3000
	for i := range commands {
		s, err := Open(dir, ReadWrite)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, "k"+strconv.Itoa(i), strings.Repeat("x", 4000))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		s.Close()
		r, err := Open(dir, ReadOnly)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := r.DB().QueryRow(`SELECT count(*) FROM meta WHERE key LIKE 'k%'`).Scan(&n); err != nil || n != i+1 {
			t.Fatalf("after command %d: %d rows, %v", i, n, err)
		}
		r.Close()
	}
	info, err := os.Stat(filepath.Join(dir, File+"-wal"))
	if err != nil {
		t.Fatalf("the log should be kept between commands: %v", err)
	}
	if info.Size() > maxLog+64<<10 {
		t.Errorf("the log has grown to %d bytes", info.Size())
	}
}
