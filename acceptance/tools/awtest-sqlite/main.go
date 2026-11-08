// Command awtest-sqlite runs SQL against a SQLite file, for scenarios that need state that aw would
// not write itself, such as a database from a newer release. It knows nothing about aw's tables.
//
//	awtest-sqlite <file> <sql>...
//	awtest-sqlite -hold <file> <started> <stop>   hold a write transaction until <stop> exists
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) == 5 && os.Args[1] == "-hold" {
		hold(os.Args[2], os.Args[3], os.Args[4])
		return
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: awtest-sqlite <file> <sql>...")
		os.Exit(2)
	}
	db, err := sql.Open("sqlite", "file:"+os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	for _, q := range os.Args[2:] {
		if _, err := db.Exec(q); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", q, err)
			os.Exit(1)
		}
	}
}

// hold takes the write lock, as a long state-changing command would, and keeps it.
func hold(file, started, stop string) {
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(started, nil, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for range 600 {
		if _, err := os.Stat(stop); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	conn.ExecContext(context.Background(), "ROLLBACK") //nolint:errcheck
}
