package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// fileIdentity tells one file from another that later appears at the same
// path. created is the file's creation (birth) time in nanoseconds, 0 if
// the filesystem doesn't report it; index is its inode or NTFS file index
// (which includes a reuse counter on NTFS), 0 if unknown.
//
// Matching (same): where both have a creation time, it decides on Linux and
// macOS: inode numbers get reused (ext4, Samba shares on ext4) and change
// across mounts on FAT/exFAT and some network shares, but a new file never
// has the old file's creation time. On Windows both must match: NTFS gives
// a file recreated under the same name within 15 s the old one's creation
// time ("tunneling"), and its file index tells them apart. Without a
// creation time the index alone decides; with neither, nothing matches.
type fileIdentity struct {
	index   uint64
	created int64
}

func (a fileIdentity) known() bool { return a.index != 0 || a.created != 0 }

func (a fileIdentity) same(b fileIdentity) bool {
	if a.created != 0 && b.created != 0 {
		if a.created != b.created {
			return false
		}
		if bothNeeded {
			return a.index != 0 && a.index == b.index
		}
		return true
	}
	return a.index != 0 && a.index == b.index
}

func (a fileIdentity) String() string {
	return strconv.FormatUint(a.index, 16) + ":" + strconv.FormatInt(a.created, 10)
}

func parseIdentity(s string) (fileIdentity, error) {
	i, c, ok := strings.Cut(s, ":")
	if !ok {
		return fileIdentity{}, fmt.Errorf("bad identity %q", s)
	}
	index, err := strconv.ParseUint(i, 16, 64)
	if err != nil {
		return fileIdentity{}, err
	}
	created, err := strconv.ParseInt(c, 10, 64)
	if err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{index, created}, nil
}
