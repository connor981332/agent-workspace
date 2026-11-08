//go:build linux || darwin

package config

import (
	"os"
	"syscall"
)

func inode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
