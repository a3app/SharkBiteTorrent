package engine

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileID identifies the file at path itself (volume serial number and file
// index), so a record of a file the store created can't vouch for a
// different file that later appears at the same path. Reparse points
// (symlinks, junctions) aren't followed.
func fileID(path string) (id string, size int64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", 0, err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", 0, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", 0, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return "", 0, fmt.Errorf("%s: not a regular file", path)
	}
	id = fmt.Sprintf("%x:%x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	size = int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return id, size, nil
}
