//go:build darwin

package indexer

import "golang.org/x/sys/unix"

// hostPhysicalMemory returns total physical RAM in bytes via the hw.memsize
// sysctl. Returns 0 when the sysctl is unavailable, so the budget logic falls
// back cleanly.
func hostPhysicalMemory() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
