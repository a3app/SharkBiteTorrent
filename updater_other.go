//go:build !windows

package main

import (
	"errors"
	"runtime"
	"strings"
)

// On Linux the package manager (pacman) owns the install, and on macOS the
// app is unsigned, so the app doesn't install updates itself. It downloads
// and verifies the right file instead (DownloadUpdate).

func canSelfUpdate(string) bool { return false }

func runInstaller(string) error { return errors.New("not supported on this platform") }

// updateAsset names the release file for this computer, "" if there's none:
// the pacman package if the app was installed with it, else the tarball.
func updateAsset(version, exe string) string {
	switch {
	case runtime.GOOS == "darwin":
		return "SharkBiteTorrent-macos-universal.zip"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		if exe == "/usr/bin/sharkbitetorrent" {
			return "sharkbitetorrent-" + version + "-1-x86_64.pkg.tar.zst"
		}
		return "SharkBiteTorrent-" + version + "-linux-x86_64.tar.gz"
	}
	return ""
}

func installHint(path string) string {
	switch {
	case strings.HasSuffix(path, ".pkg.tar.zst"):
		return "Install it with: sudo pacman -U '" + path + "'"
	case strings.HasSuffix(path, ".zip"):
		return "Open it and drag Shark BiteTorrent into Applications, replacing the old copy."
	}
	return "Extract it and replace your SharkBiteTorrent file with the new one."
}
