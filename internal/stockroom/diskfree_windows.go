//go:build windows

package stockroom

import (
	"syscall"
	"unsafe"
)

// freeSpace reports the bytes available to this process on the volume holding
// dir. The error is returned rather than folded into a zero -- see the unix
// copy of this file for why the distinction matters.
//
// GetDiskFreeSpaceExW through a lazy DLL rather than golang.org/x/sys/windows,
// so the photo mirror's disk check adds no module to go.mod. The first of the
// three out-parameters is the one that accounts for per-user quotas, which is
// the number that decides whether *this* process can write.
var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func freeSpace(dir string) (int64, error) {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, totalFree uint64
	r, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, callErr
	}
	return int64(freeToCaller), nil
}
