package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBundle makes an ad-hoc signed .app with the given id and version.
func fakeBundle(t *testing.T, dir, id, version, program string) string {
	t.Helper()
	app := filepath.Join(dir, "SharkBiteTorrent.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + id + `</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
<key>CFBundleExecutable</key><string>SharkBiteTorrent</string>
</dict></plist>`
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "SharkBiteTorrent"), []byte("#!/bin/sh\necho "+program+"\n"), 0o755)
	if out, err := exec.Command("codesign", "--force", "--deep", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v %s", err, out)
	}
	return app
}

func zipBundle(t *testing.T, app string) string {
	t.Helper()
	zip := filepath.Join(t.TempDir(), "SharkBiteTorrent-macos-universal.zip")
	if out, err := exec.Command("ditto", "-c", "-k", "--keepParent", app, zip).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v %s", err, out)
	}
	return zip
}

// The update replaces the running .app in place and relaunches it; one
// that isn't this app, or another version, or with a broken signature,
// leaves the old one alone.
func TestApplyUpdateDarwin(t *testing.T) {
	var relaunched []string
	old := relaunchAfterExit
	t.Cleanup(func() { relaunchAfterExit = old })
	relaunchAfterExit = func(cmd ...string) error { relaunched = cmd; return nil }

	apps := t.TempDir()
	current := fakeBundle(t, apps, bundleID, "0.2.38", "old")
	exe := filepath.Join(current, "Contents", "MacOS", "SharkBiteTorrent")
	program := func() string { b, _ := os.ReadFile(exe); return string(b) }
	if !canSelfUpdate(exe) {
		t.Fatal("can't update a .app in a folder of the user's own")
	}

	for _, bad := range []struct{ id, version, why string }{
		{"com.example.other", "0.2.39", "another app"},
		{bundleID, "0.2.40", "another version"},
	} {
		zip := zipBundle(t, fakeBundle(t, t.TempDir(), bad.id, bad.version, "bad"))
		if err := applyUpdate(zip, "0.2.39", exe); err == nil || !strings.Contains(program(), "old") {
			t.Errorf("%s was installed (err %v)", bad.why, err)
		}
	}
	tampered := fakeBundle(t, t.TempDir(), bundleID, "0.2.39", "new")
	os.WriteFile(filepath.Join(tampered, "Contents", "MacOS", "SharkBiteTorrent"), []byte("#!/bin/sh\necho tampered\n"), 0o755)
	if err := applyUpdate(zipBundle(t, tampered), "0.2.39", exe); err == nil || !strings.Contains(program(), "old") {
		t.Errorf("an app whose signature doesn't match was installed (err %v)", err)
	}
	if relaunched != nil {
		t.Fatal("relaunched after a failed update")
	}

	zip := zipBundle(t, fakeBundle(t, t.TempDir(), bundleID, "0.2.39", "new"))
	if err := applyUpdate(zip, "0.2.39", exe); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(program(), "new") {
		t.Error("the .app wasn't replaced")
	}
	if len(relaunched) == 0 || relaunched[len(relaunched)-1] != current {
		t.Errorf("relaunch: %v", relaunched)
	}
	left, _ := filepath.Glob(filepath.Join(apps, "*"))
	if len(left) != 1 {
		t.Errorf("left beside the app: %v", left)
	}
}

func TestCanSelfUpdateDarwin(t *testing.T) {
	if canSelfUpdate("/usr/local/bin/SharkBiteTorrent") {
		t.Error("a program that isn't in a .app")
	}
	if canSelfUpdate("/System/Applications/Calculator.app/Contents/MacOS/Calculator") {
		t.Error("a .app in a folder the user can't write to")
	}
	ww := t.TempDir()
	os.Chmod(ww, 0o777)
	app := fakeBundle(t, ww, bundleID, "0.2.38", "x")
	if canSelfUpdate(filepath.Join(app, "Contents", "MacOS", "SharkBiteTorrent")) {
		t.Error("a .app in a folder anyone can write to")
	}
}
