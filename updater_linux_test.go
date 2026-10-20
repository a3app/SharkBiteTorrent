package main

import "testing"

// The file offered matches how the app was installed.
func TestUpdateAssetLinux(t *testing.T) {
	if got := updateAsset("0.2.37", "/usr/bin/sharkbitetorrent"); got != "sharkbitetorrent-0.2.37-1-x86_64.pkg.tar.zst" {
		t.Errorf("pacman install: %q", got)
	}
	if got := updateAsset("0.2.37", "/home/a/apps/SharkBiteTorrent"); got != "SharkBiteTorrent-0.2.37-linux-x86_64.tar.gz" {
		t.Errorf("tarball: %q", got)
	}
}
