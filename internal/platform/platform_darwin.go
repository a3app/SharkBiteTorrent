package platform

import (
	"errors"
	"sharkbite/internal/systool"
)

// On macOS, URL schemes and document types are declared in the app bundle's
// Info.plist (see build/darwin), and Launch Services registers them when the
// app is installed, so there is nothing to do at runtime.

func OpenFolder(dir string) error { return systool.Command("open", dir).Start() }

func RegisterAssociations(string) error   { return nil }
func UnregisterAssociations(string) error { return nil }

func Associations(string) AssocStatus { return AssocStatus{Supported: false} }

func OpenDefaultAppsSettings() error {
	return errors.New("not needed on macOS")
}

// RevealPath shows a file selected in Finder, or opens a folder.
func RevealPath(path string, isDir bool) error {
	if isDir {
		return systool.Command("open", path).Start()
	}
	return systool.Command("open", "-R", path).Start()
}
