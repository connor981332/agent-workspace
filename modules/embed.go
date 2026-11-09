// Package modules holds the first-party module content (architecture.md, "Modules"). It is data:
// each directory has a module.toml, optional instructions, and the files it installs.
package modules

import "embed"

// FS is the first-party modules.
//
//go:embed all:*
var FS embed.FS
