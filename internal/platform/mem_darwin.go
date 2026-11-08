package platform

import "golang.org/x/sys/unix"

// TotalMemory is the machine's memory in bytes, or 0 if it cannot be read.
func TotalMemory() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
