package engine

import "golang.org/x/sys/unix"

// fsType is the type of the filesystem dir is on (smbfs, apfs…).
func fsType(dir string) (uint64, bool) {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return 0, false
	}
	return uint64(st.Type), true
}
