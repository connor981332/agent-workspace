//go:build ncruces && !snapshot

package main

import _ "github.com/ncruces/go-sqlite3/driver"

const driverName = "sqlite3"

func dsn(path string) string { return "file:" + path + "?_pragma=busy_timeout(50)" }
