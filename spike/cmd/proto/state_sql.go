//go:build !snapshot

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
)

func readState(dir, session string) (string, int, error) {
	db, err := sql.Open(driverName, dsn(filepath.Join(dir, "state.db")))
	if err != nil {
		return "", 0, err
	}
	defer db.Close()
	var lane string
	var generation int
	err = db.QueryRow(`SELECT lane, generation FROM attachments WHERE session_id = ?`, session).Scan(&lane, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		lane = "none"
	} else if err != nil {
		return "", 0, err
	}
	var notes int
	if err := db.QueryRow(`SELECT count(*) FROM notifications WHERE session_id = ? AND delivered = 0`, session).Scan(&notes); err != nil {
		return "", 0, err
	}
	return lane, notes, nil
}

func setupState(dir string) error {
	db, err := sql.Open(driverName, dsn(filepath.Join(dir, "state.db")))
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS lanes (number INTEGER PRIMARY KEY, name TEXT, state TEXT)`,
		`CREATE TABLE IF NOT EXISTS attachments (session_id TEXT PRIMARY KEY, lane TEXT, generation INTEGER)`,
		`CREATE TABLE IF NOT EXISTS notifications (id INTEGER PRIMARY KEY, session_id TEXT, body TEXT, delivered INTEGER)`,
		`CREATE INDEX IF NOT EXISTS notifications_session ON notifications (session_id, delivered)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	for i := 1; i <= 10; i++ {
		if _, err := db.Exec(`INSERT OR REPLACE INTO lanes VALUES (?, ?, 'active')`, i, "lane-"+strconv.Itoa(i)); err != nil {
			return err
		}
	}
	for i := 0; i < 50; i++ {
		sid := fmt.Sprintf("session-%02d", i)
		if _, err := db.Exec(`INSERT OR REPLACE INTO attachments VALUES (?, ?, ?)`, sid, fmt.Sprintf("%02d", i%10+1), i); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO notifications (session_id, body, delivered) VALUES (?, 'seat granted', ?)`, sid, i%2); err != nil {
			return err
		}
	}

	return nil
}
