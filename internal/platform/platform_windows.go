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
