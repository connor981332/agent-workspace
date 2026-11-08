// Package state is the workspace's SQLite state store (architecture.md): WAL mode, the schema
// version in PRAGMA user_version, numbered migrations with a backup before each, and read-only mode
// for a database this binary cannot safely write (R19).
//
// The store lives in <workspace git dir>/aw/state.db, shared by every worktree. Git stays the
// source of truth for code; the store records what Git cannot.
//
// This package is on the hot path: it may not import os/exec or net.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the binary stays static
)

// File is the database's name in the state directory.
const File = "state.db"

// Mode says what the caller intends.
type Mode int

const (
	// ReadOnly never creates, migrates or writes. A missing database is ErrNoState.
	ReadOnly Mode = iota
	// ReadWrite creates the database if needed and migrates it forward, after a backup.
	ReadWrite
)

// ErrNoState means the workspace has no state database yet (before setup).
var ErrNoState = errors.New("the workspace has no state yet → run bin/aw setup")

// ReadOnlyError explains why a store is read-only. Every write returns it.
type ReadOnlyError struct {
	Path   string
	Reason string
	Fix    string
}

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("%s is read-only: %s → %s", e.Path, e.Reason, e.Fix)
}

// Store is an open state database.
type Store struct {
	db       *sql.DB
	path     string
	version  int // PRAGMA user_version as found (after migration, for ReadWrite)
	readOnly *ReadOnlyError
}

// Open opens the store in dir.
//
// A database with a newer schema than this binary knows is opened read-only, whatever the mode:
// this binary cannot know what the newer records mean, so it protects them. A file that is not a
// readable SQLite database is an *UnreadableError: the caller must treat everything it describes
// as protected.
func Open(dir string, mode Mode) (*Store, error) {
	path := filepath.Join(dir, File)
	_, statErr := os.Stat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist) && mode == ReadOnly:
		return nil, ErrNoState
	case errors.Is(statErr, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating the state directory %s: %w → check that the workspace's .git directory is writable", dir, err)
		}
	case statErr != nil:
		return nil, &UnreadableError{Path: path, Err: statErr}
	}

	db, err := sql.Open("sqlite", dsn(path, mode))
	if err != nil {
		return nil, &UnreadableError{Path: path, Err: err}
	}
	// One connection: aw is a short-lived process, and SQLite serializes writers anyway.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}

	if mode == ReadWrite {
		if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, err
		}
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&s.version); err != nil {
		db.Close()
		return nil, &UnreadableError{Path: path, Err: err}
	}
	// Reading the schema table checks that this really is a SQLite database.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema`).Scan(&n); err != nil {
		db.Close()
		return nil, &UnreadableError{Path: path, Err: err}
	}

	switch {
	case s.version > SchemaVersion:
		s.readOnly = &ReadOnlyError{Path: path,
			Reason: fmt.Sprintf("it has state schema %d, newer than this aw's %d", s.version, SchemaVersion),
			Fix:    "run the workspace's pinned aw (bin/aw), or upgrade the pin; nothing in this state will be changed"}
	case mode == ReadOnly:
		// An older schema is read as it is; the next writer migrates it.
		s.readOnly = &ReadOnlyError{Path: path, Reason: "opened for reading", Fix: "this is a read-only command"}
	default:
		if err := s.migrate(); err != nil {
			db.Close()
			return nil, err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, err
		}
	}
	if s.readOnly != nil {
		if _, err := db.Exec(`PRAGMA query_only = 1`); err != nil {
			db.Close()
			return nil, &UnreadableError{Path: path, Err: err}
		}
	}
	return s, nil
}

func dsn(path string, mode Mode) string {
	q := []string{"_pragma=busy_timeout(5000)", "_txlock=immediate"}
	if mode == ReadWrite {
		q = append(q, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)", "_pragma=foreign_keys(1)")
	}
	return "file:" + path + "?" + strings.Join(q, "&")
}

// UnreadableError means the database cannot be read. Everything it describes must be protected.
type UnreadableError struct {
	Path string
	Err  error
}

func (e *UnreadableError) Error() string {
	return fmt.Sprintf("%s cannot be read (%v), so every lane and lease is treated as protected → run aw doctor, and restore it from the newest %s.bak-* backup if it is damaged",
		e.Path, e.Err, File)
}

func (e *UnreadableError) Unwrap() error { return e.Err }

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Version is the schema version of the database.
func (s *Store) Version() int { return s.version }

// ReadOnly returns why the store is read-only, or nil.
func (s *Store) ReadOnly() *ReadOnlyError { return s.readOnly }

// Write runs fn in one IMMEDIATE transaction: it takes the write lock at the start, waits at most
// the busy timeout for it, and commits only if fn returns nil.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if s.readOnly != nil {
		return s.readOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DB is the underlying database, for queries.
func (s *Store) DB() *sql.DB { return s.db }

// Meta returns a value from the meta table, or "" if it is not set.
func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta records a value in the meta table.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
