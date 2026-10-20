package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Linux desktops (KDE, GNOME, Xfce, …) start the .desktop entries in
// ~/.config/autostart at sign-in (the XDG Autostart spec).

func autostartPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", desktopID)
}

var execLine = regexp.MustCompile(`(?m)^Exec="([^"]*)"`)

func StartupEnabled() bool {
	b, err := os.ReadFile(autostartPath())
	if err != nil {
		return false
	}
	s := string(b)
	return !strings.Contains(s, "\nHidden=true") && !strings.Contains(s, "\nX-GNOME-Autostart-enabled=false")
}

func SetStartup(exe string, on bool) error {
	p := autostartPath()
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Shark BiteTorrent
Comment=Start Shark BiteTorrent in the tray
Exec="%s" %s
Icon=sharkbitetorrent
Terminal=false
X-GNOME-Autostart-enabled=true
`, exe, AutostartArg)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(entry), 0o644)
}

func RefreshStartup(exe string) {
	b, err := os.ReadFile(autostartPath())
	if err != nil {
		return
	}
	if m := execLine.FindSubmatch(b); m != nil {
		if _, err := os.Stat(string(m[1])); err != nil && StartupEnabled() {
			SetStartup(exe, true)
		}
	}
}
