//go:build !windows

package stockroom

import "syscall"

// freeSpace reports the bytes available on the filesystem holding dir, or 0
// when it cannot be measured.
//
// Zero means "unknown" rather than "full", and every caller treats it that
// way: a free-space warning that fires because the measurement failed is a
// warning about the measurement, and the person reading it has no way to tell.
//
// Bavail rather than Bfree: Bfree counts blocks reserved for root, which the
// server is not, so it would report room that cannot actually be written to.
func freeSpace(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
