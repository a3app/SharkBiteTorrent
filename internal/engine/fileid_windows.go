package engine

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const bothNeeded = true // see fileIdentity.same (NTFS tunneling)

// identityOf reads the file at path without following a reparse point.
func identityOf(path string) (fileIdentity, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileIdentity{}, err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fileIdentity{}, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fileIdentity{}, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return fileIdentity{}, fmt.Errorf("%s: not a regular file", path)
	}
	id := fileIdentity{index: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}
	// A zero FILETIME means "unknown"; Nanoseconds() would turn it into a
	// large negative number instead of 0.
	if info.CreationTime.HighDateTime != 0 || info.CreationTime.LowDateTime != 0 {
		id.created = info.CreationTime.Nanoseconds()
	}
	return id, nil
}
