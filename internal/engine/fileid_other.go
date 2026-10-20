//go:build !linux && !darwin && !windows

package engine

import (
	"fmt"
	"os"
	"syscall"
)

const bothNeeded = false

func identityOf(path string) (fileIdentity, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() {
		return fileIdentity{}, fmt.Errorf("%s: not a regular file", path)
	}
	return fileIdentity{index: uint64(st.Ino)}, nil
}
