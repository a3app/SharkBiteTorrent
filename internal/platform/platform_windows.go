package platform

import (
	"os/exec"

	"sharkbite/internal/winassoc"
)

func OpenFolder(dir string) error { return exec.Command("explorer", dir).Start() }

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
		return exec.Command("explorer", path).Start()
	}
	return exec.Command("explorer", "/select,"+path).Start()
}
