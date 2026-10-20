package engine

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const bothNeeded = false // see fileIdentity.same

// identityOf reads the file at path without following a final symlink.
func identityOf(path string) (fileIdentity, error) {
	var st unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME|unix.STATX_TYPE, &st); err != nil {
		return fileIdentity{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fileIdentity{}, fmt.Errorf("%s: not a regular file", path)
	}
	id := fileIdentity{index: st.Ino}
	if st.Mask&unix.STATX_BTIME != 0 {
		id.created = st.Btime.Sec*1e9 + int64(st.Btime.Nsec)
	}
	return id, nil
}
