package platform

import (
	"syscall"

	"sharkbite/internal/systool"

	"sharkbite/internal/winassoc"
)

func OpenFolder(dir string) error { return systool.Command("explorer", dir).Start() }

func RegisterAssociations(exe string) error   { return winassoc.Register(exe) }
func UnregisterAssociations(exe string) error { return winassoc.Unregister(exe) }
func OpenDefaultAppsSettings() error          { return winassoc.OpenDefaultAppsSettings() }

func Associations(exe string) AssocStatus {
	s := winassoc.Status(exe)
	return AssocStatus{Supported: true, Magnet: s.Magnet, Torrent: s.Torrent, CanOpenSettings: true}
}

// RevealPath shows a file selected in Explorer, or opens a folder.
func RevealPath(path string, isDir bool) error {
	if isDir {
		return systool.Command("explorer", path).Start()
	}
	// Explorer only understands /select,"C:\path with spaces\file" with the
	// quotes around the path alone. Go's argument quoting wraps the whole
	// "/select,..." argument instead, which Explorer can't parse (it falls
	// back to opening Documents), so pass the command line verbatim.
	cmd := systool.Command("explorer")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer /select,"` + path + `"`}
	return cmd.Start()
}
