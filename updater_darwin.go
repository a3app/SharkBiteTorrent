package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

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
	if err := swapBundle(newApp, bundle); err != nil {
		return err
	}
	// The old app is now at newApp, in dir, which is removed on return; the
	// running copy keeps working from its deleted files until it quits.
	return relaunchAfterExit("/usr/bin/open", "-n", bundle)
}

// swapBundle puts newApp in bundle's place and the old one at newApp, in one
// step (RENAME_SWAP, on APFS and HFS+), so a crash can't leave no app. A
// disk that can't (a network drive) gets two renames instead.
func swapBundle(newApp, bundle string) error {
	err := unix.RenamexNp(newApp, bundle, unix.RENAME_SWAP)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EINVAL) {
		return err
	}
	old := bundle + ".old-update"
	os.RemoveAll(old) // left by an update that stopped partway
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(newApp, bundle); err != nil {
		if rerr := os.Rename(old, bundle); rerr != nil {
			return fmt.Errorf("installing the update failed (%v), and so did putting the old app back (%v): it's at %s", err, rerr, old)
		}
		return err
	}
	// So it goes with the update's folder; if not, cleanUpdateLeftovers
	// removes it at a later start. The update itself is in place.
	os.Rename(old, newApp)
	return nil
}

// cleanUpdateLeftovers removes what an update that stopped partway (the app
// was killed) left next to the app: its hidden folder, and the old copy two
// renames leave. Over an hour old only, so a running update isn't touched.
func cleanUpdateLeftovers(exe string) {
	b := bundleOf(exe)
	if b == "" {
		return
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(b), ".SharkBiteTorrent-update-*"))
	for _, p := range append(left, b+".old-update") {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > time.Hour {
			os.RemoveAll(p)
		}
	}
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
