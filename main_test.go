package main

import (
	"os"
	"testing"
)

// Tests save secrets (remote pairings); on Linux and macOS without a keyring
// that creates a key file in the config folder. Point the config folder (and
// home, which macOS uses for it) at a temporary one so tests never touch the
// user's real settings. Windows uses DPAPI and needs nothing.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sharkbite-test")
	if err == nil {
		os.Setenv("XDG_CONFIG_HOME", dir)
		if os.PathSeparator == '/' {
			os.Setenv("HOME", dir)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
