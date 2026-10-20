package engine

import (
	"fmt"
	"os"
	"syscall"
)

const bothNeeded = false // see fileIdentity.same

// identityOf reads the file at path without following a final symlink.
func identityOf(path string) (fileIdentity, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() {
		return fileIdentity{}, fmt.Errorf("%s: not a regular file", path)
	}
	return fileIdentity{index: st.Ino, created: st.Birthtimespec.Sec*1e9 + st.Birthtimespec.Nsec}, nil
}
