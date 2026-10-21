package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func autostartFile() string { return autostartPath() }

func TestDesktopExec(t *testing.T) {
	if got, err := desktopExec("/home/a/Shark & Bite/app"); err != nil || got != `"/home/a/Shark & Bite/app"` {
		t.Errorf("plain path: %q, %v", got, err)
	}
	for _, p := range []string{`/a/"b`, "/a/`b", "/a/$b", `/a/\b`, "/a/100%/b", "/a/b\nX-Evil=1"} {
		if _, err := desktopExec(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

// The packaged app removes a per-user entry that would hide the package's
// own; a copy run from elsewhere points its entry at where it is now.
func TestRefreshAssociations(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	wwd := inWorldWritableDir
	t.Cleanup(func() { inWorldWritableDir = wwd })
	inWorldWritableDir = func(string) bool { return false } // t.TempDir is under /tmp

	// Another copy that still exists keeps magnet links.
	other := filepath.Join(t.TempDir(), "SharkBiteTorrent")
	os.WriteFile(other, nil, 0o755)
	if err := writeEntry(other); err != nil {
		t.Fatal(err)
	}
	RefreshAssociations(filepath.Join(t.TempDir(), "SharkBiteTorrent"))
	if b, _ := os.ReadFile(desktopPath()); !strings.Contains(string(b), "\nExec=\""+other+"\" %u") {
		t.Errorf("a second copy took over magnet links:\n%s", b)
	}

	// A copy in a folder anyone can write to never takes them, even from a
	// copy that's gone.
	old := filepath.Join(t.TempDir(), "SharkBiteTorrent")
	if err := writeEntry(old); err != nil {
		t.Fatal(err)
	}
	inWorldWritableDir = func(string) bool { return true }
	RefreshAssociations(filepath.Join(t.TempDir(), "SharkBiteTorrent"))
	if b, _ := os.ReadFile(desktopPath()); !strings.Contains(string(b), "\nExec=\""+old+"\" %u") {
		t.Errorf("a copy in a world-writable folder took over magnet links:\n%s", b)
	}
	inWorldWritableDir = func(string) bool { return false }

	// The copy the entry names is gone (moved): the moved copy takes over.
	moved := filepath.Join(t.TempDir(), "SharkBiteTorrent")
	RefreshAssociations(moved)
	if b, _ := os.ReadFile(desktopPath()); !strings.Contains(string(b), "\nExec=\""+moved+"\" %u") {
		t.Errorf("entry not updated:\n%s", b)
	}
	RefreshAssociations(packagedExe)
	if _, err := os.Stat(desktopPath()); !os.IsNotExist(err) {
		t.Error("per-user entry left hiding the package's")
	}
}

// Sign-in start set up by a copy run from elsewhere (a tarball) moves to the
// package once it's installed, even though that copy still exists; between
// two copies run from elsewhere, it stays with the one that set it up.
func TestRefreshStartupPackaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	tarball := filepath.Join(home, "SharkBiteTorrent")
	os.WriteFile(tarball, nil, 0o755)
	if err := SetStartup(tarball, true); err != nil {
		t.Fatal(err)
	}
	RefreshStartup(filepath.Join(home, "another copy"))
	if b, _ := os.ReadFile(autostartPath()); !strings.Contains(string(b), tarball) {
		t.Errorf("another copy took over sign-in start:\n%s", b)
	}
	RefreshStartup(packagedExe)
	if b, _ := os.ReadFile(autostartPath()); !strings.Contains(string(b), `Exec="`+packagedExe+`"`) {
		t.Errorf("package didn't take over sign-in start:\n%s", b)
	}
}
