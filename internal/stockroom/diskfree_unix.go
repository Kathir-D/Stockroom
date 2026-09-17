//go:build !windows

package stockroom

import "syscall"

// freeSpace reports the bytes available on the filesystem holding dir.
//
// The error is returned rather than folded into a zero, because a failed
// measurement and a genuinely full disk are different facts and the caller
// acts differently on each: an unmeasurable disk earns a warning about the
// measurement, while zero bytes available is the low-space alarm doing its
// job. Collapsing the two meant the alarm was skipped precisely when the disk
// was full.
//
// Bavail rather than Bfree: Bfree counts blocks reserved for root, which the
// server is not, so it would report room that cannot actually be written to.
func freeSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
