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

// RevealPath shows the file or folder selected in Finder. Always -R: plain
// "open" on a folder named like a bundle (Foo.app) would launch it.
func RevealPath(path string, _ bool) error {
	return systool.Command("open", "-R", path).Start()
}

// OpenURL opens a web link in the default browser.
func OpenURL(link string) error { return systool.Command("open", link).Start() }
