package engine

import (
	"os"

	"golang.org/x/sys/windows"
)

// makeSparse marks a new file sparse, so writing far past the end doesn't
// make the filesystem (or a NAS over SMB) zero-fill the gap first. Measured
// on a NAS: random writes went from ~0 MB/s to normal speed.
func makeSparse(f *os.File) error {
	var n uint32
	return windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil)
}
