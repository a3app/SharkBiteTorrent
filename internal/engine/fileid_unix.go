//go:build !windows

package engine

import (
	"fmt"
	"os"
	"syscall"
)

// fileID identifies the file at path itself (device and inode), so a record
// of a file the store created can't vouch for a different file that later
// appears at the same path. Symlinks aren't followed.
func fileID(path string) (id string, size int64, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s: not a regular file", path)
	}
	return fmt.Sprintf("%x:%x", uint64(st.Dev), uint64(st.Ino)), fi.Size(), nil
}
