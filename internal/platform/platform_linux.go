package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sharkbite/internal/systool"
)

// Linux follows the freedesktop.org standards that KDE (CachyOS default),
// GNOME and others implement: a .desktop entry declaring the MIME types, made
// default with xdg-mime. Distro packages ship their own .desktop file in
// /usr/share/applications; this per-user one covers running a bare binary.

const desktopID = "sharkbitetorrent.desktop"

var mimeTypes = []string{"x-scheme-handler/magnet", "application/x-bittorrent"}

func OpenFolder(dir string) error { return systool.Command("xdg-open", dir).Start() }

func desktopPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "applications", desktopID)
}

func RegisterAssociations(exe string) error {
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Shark BiteTorrent
Comment=BitTorrent client with a built-in VPN
Exec="%s" %%u
Icon=sharkbitetorrent
Terminal=false
Categories=Network;FileTransfer;P2P;
MimeType=%s;
`, exe, strings.Join(mimeTypes, ";"))
	p := desktopPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(entry), 0o644); err != nil {
		return err
	}
	systool.Command("update-desktop-database", filepath.Dir(p)).Run() // optional helper
	for _, m := range mimeTypes {
		if out, err := systool.Command("xdg-mime", "default", desktopID, m).CombinedOutput(); err != nil {
			return fmt.Errorf("xdg-mime default %s: %v: %s", m, err, out)
		}
	}
	return nil
}

func UnregisterAssociations(string) error {
	err := os.Remove(desktopPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func isDefault(mime string) bool {
	out, err := systool.Command("xdg-mime", "query", "default", mime).Output()
	return err == nil && strings.TrimSpace(string(out)) == desktopID
}

func Associations(string) AssocStatus {
	return AssocStatus{
		Supported: true,
		Magnet:    isDefault("x-scheme-handler/magnet"),
		Torrent:   isDefault("application/x-bittorrent"),
	}
}

func OpenDefaultAppsSettings() error {
	return errors.New("use your desktop's Default Applications settings")
}

// RevealPath opens the folder (or the folder containing the file). There's
// no portable "select this file" across Linux file managers.
func RevealPath(path string, isDir bool) error {
	if !isDir {
		path = filepath.Dir(path)
	}
	return systool.Command("xdg-open", path).Start()
}
