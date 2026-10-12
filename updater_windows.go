package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// canSelfUpdate is true for the installed version (the installer puts
// uninstall.exe next to the app). The portable exe can't replace itself
// while running, so it gets a download link instead.
func canSelfUpdate(exe string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(exe), "uninstall.exe"))
	return err == nil
}

// runInstaller starts the downloaded installer silently. It closes the
// running app, upgrades in place and relaunches it.
func runInstaller(path string) error {
	return exec.Command(path, "/S").Start()
}
