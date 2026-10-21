package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sharkbite/internal/systool"
)

const bundleID = "com.sharkbytesoftware.sharkbitetorrent"

// bundleOf is the .app the running executable is in ("" if it isn't in one).
func bundleOf(exe string) string {
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	return exe[:i+len(".app")]
}

// canSelfUpdate: the app can replace its own .app. Not when it runs from
// somewhere read-only (a disk image, or App Translocation: macOS runs a
// downloaded app from a read-only copy until it's moved), or from a folder
// anyone can write to; then it downloads the update for the user instead.
func canSelfUpdate(exe string) bool {
	b := bundleOf(exe)
	return b != "" && canReplace(filepath.Dir(b)) && canReplace(b)
}

// applyUpdate replaces the running app's .app with the one in zip (already
// checked against the release's signed checksums) and starts it once this
// process has quit. The new copy is unpacked next to the old one, checked
// to be this app at that version with an intact signature, and swapped in
// by renaming, so a failure leaves the old one in place.
func applyUpdate(zip, version, exe string) error {
	bundle := bundleOf(exe)
	if bundle == "" {
		return errors.New("the app isn't in a .app")
	}
	dir, err := os.MkdirTemp(filepath.Dir(bundle), ".SharkBiteTorrent-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// ditto keeps what an .app needs (symlinks, attributes, the signature).
	if out, err := systool.Command("ditto", "-x", "-k", zip, dir).CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking the update: %v: %s", err, strings.TrimSpace(string(out)))
	}
	apps, _ := filepath.Glob(filepath.Join(dir, "*.app"))
	if len(apps) != 1 {
		return fmt.Errorf("the update has %d apps in it, not one", len(apps))
	}
	newApp := apps[0]
	if err := checkBundle(newApp, version); err != nil {
		return fmt.Errorf("the update isn't usable: %w", err)
	}
	old := bundle + ".old-update"
	os.RemoveAll(old) // left by an update that stopped partway
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(newApp, bundle); err != nil {
		os.Rename(old, bundle)
		return err
	}
	// The running copy keeps working from the deleted files until it quits.
	os.RemoveAll(old)
	return relaunchAfterExit("/usr/bin/open", "-n", bundle)
}

// checkBundle makes sure app is this app, at version, with a signature
// that matches its contents (macOS won't start it otherwise).
func checkBundle(app, version string) error {
	plist := filepath.Join(app, "Contents", "Info.plist")
	read := func(key string) string {
		out, _ := systool.Command("plutil", "-extract", key, "raw", "-o", "-", plist).Output()
		return strings.TrimSpace(string(out))
	}
	if id := read("CFBundleIdentifier"); id != bundleID {
		return fmt.Errorf("it's %q, not this app", id)
	}
	if v := read("CFBundleShortVersionString"); v != version {
		return fmt.Errorf("it's version %q, not %s", v, version)
	}
	if fi, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "SharkBiteTorrent")); err != nil || fi.Mode()&0o111 == 0 {
		return errors.New("its program is missing")
	}
	if out, err := systool.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		return fmt.Errorf("its signature doesn't match: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
