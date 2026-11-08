// Command awtest-sqlite runs SQL against a SQLite file, for scenarios that need state that aw would
// not write itself, such as a database from a newer release. It knows nothing about aw's tables.
//
//	awtest-sqlite <file> <sql>...
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
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
