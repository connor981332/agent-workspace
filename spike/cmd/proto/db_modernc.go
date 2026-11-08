//go:build !ncruces && !snapshot

package main

import _ "modernc.org/sqlite"

const driverName = "sqlite"

func dsn(path string) string { return "file:" + path + "?_pragma=busy_timeout(50)" }
