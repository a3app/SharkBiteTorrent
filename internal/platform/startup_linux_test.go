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
	old := filepath.Join(t.TempDir(), "SharkBiteTorrent")
	if err := writeEntry(old); err != nil {
		t.Fatal(err)
	}
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
