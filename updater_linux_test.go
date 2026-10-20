package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The file offered matches how the app was installed.
func TestUpdateAssetLinux(t *testing.T) {
	if got := updateAsset("0.2.37", "/usr/bin/sharkbitetorrent"); got != "sharkbitetorrent-0.2.37-1-x86_64.pkg.tar.zst" {
		t.Errorf("pacman install: %q", got)
	}
	if got := updateAsset("0.2.37", "/home/a/apps/SharkBiteTorrent"); got != "SharkBiteTorrent-0.2.37-linux-x86_64.tar.gz" {
		t.Errorf("tarball: %q", got)
	}
}

func TestFreeNameAndHint(t *testing.T) {
	dir := t.TempDir()
	name := "sharkbitetorrent-0.2.37-1-x86_64.pkg.tar.zst"
	if got := freeName(dir, name); got != filepath.Join(dir, name) {
		t.Errorf("free: %q", got)
	}
	os.WriteFile(filepath.Join(dir, name), nil, 0o644)
	if got := freeName(dir, name); got != filepath.Join(dir, "sharkbitetorrent-0.2.37-1-x86_64 (2).pkg.tar.zst") {
		t.Errorf("taken: %q", got)
	}
	if got := installHint("/home/o'brien/Downloads/x.pkg.tar.zst"); got != `Install it with: sudo pacman -U '/home/o'\''brien/Downloads/x.pkg.tar.zst'` {
		t.Errorf("hint: %s", got)
	}
}
