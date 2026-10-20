//go:build linux || darwin

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartupUnix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	exe := filepath.Join(t.TempDir(), "Shark & Bite")
	os.WriteFile(exe, nil, 0o755)

	if StartupEnabled() {
		t.Fatal("enabled before turning it on")
	}
	if err := SetStartup(exe, true); err != nil || !StartupEnabled() {
		t.Fatalf("turning it on: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other")
	os.WriteFile(other, nil, 0o755)
	os.Remove(exe)
	RefreshStartup(other) // the app moved
	if !StartupEnabled() {
		t.Error("refresh turned it off")
	}
	if err := SetStartup(other, false); err != nil || StartupEnabled() {
		t.Errorf("turning it off: %v", err)
	}
}
