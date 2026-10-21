package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// packagedExe is where the pacman package installs the app: pacman updates
// that one (the app downloads the package for the user).
const packagedExe = "/usr/bin/sharkbitetorrent"

// canSelfUpdate: a copy from the tarball can replace its own program,
// unless it's in a folder it can't write to or anyone can.
func canSelfUpdate(exe string) bool {
	return exe != packagedExe && canReplace(filepath.Dir(exe))
}

// tarballFiles are the files of the tarball the update replaces, next to
// the program: the program itself, and its licenses if they're there.
var tarballFiles = []string{"SharkBiteTorrent", "LICENSE", "THIRD_PARTY_LICENSES.txt"}

// applyUpdate replaces the running program (and its licenses) with those
// in the tarball (already checked against the release's signed checksums)
// and starts it once this process has quit. Each file is written next to
// the old one and renamed over it; Linux keeps the running program's old
// file until it exits.
func applyUpdate(tarball, version, exe string) error {
	dir := filepath.Dir(exe)
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	got := map[string]string{} // name → the new file, written next to the old
	defer func() {
		for _, p := range got {
			os.Remove(p) // the ones not renamed into place
		}
	}()
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the update: %w", err)
		}
		// "SharkBiteTorrent-0.2.39-linux-x86_64/SharkBiteTorrent": only files
		// directly in the release's one folder count.
		top, name := path.Split(path.Clean(h.Name))
		if h.Typeflag != tar.TypeReg || !releaseFolder(top) || !wanted(name) || got[name] != "" {
			continue
		}
		mode := os.FileMode(0o644)
		if name == "SharkBiteTorrent" {
			mode = 0o755
		} else if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			continue // a license that isn't here: this copy was set up without it
		}
		const maxFile = 300 << 20
		if h.Size < 0 || h.Size > maxFile {
			return fmt.Errorf("the update's %s is %d bytes, more than expected", name, h.Size)
		}
		tmp, err := os.CreateTemp(dir, "."+name+".update-")
		if err != nil {
			return err
		}
		got[name] = tmp.Name()
		n, err := io.Copy(tmp, io.LimitReader(tr, maxFile))
		if err == nil && n != h.Size {
			err = fmt.Errorf("the update's %s is incomplete (%d of %d bytes)", name, n, h.Size)
		}
		if err == nil {
			err = tmp.Sync()
		}
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(tmp.Name(), mode)
		}
		if err != nil {
			return err
		}
	}
	if got["SharkBiteTorrent"] == "" {
		return errors.New("the update has no SharkBiteTorrent program in it")
	}
	// The program last: if anything fails before it, the old one runs on.
	for _, name := range append(tarballFiles[1:], tarballFiles[0]) {
		if p := got[name]; p != "" {
			target := filepath.Join(dir, name)
			if name == "SharkBiteTorrent" {
				target = exe // whatever this copy's program is called
			}
			if err := os.Rename(p, target); err != nil {
				return err
			}
			delete(got, name)
		}
	}
	return relaunchAfterExit(exe)
}

func releaseFolder(top string) bool {
	t := path.Clean(top)
	return !strings.Contains(t, "/") && strings.HasPrefix(t, "SharkBiteTorrent-")
}

func wanted(name string) bool {
	for _, n := range tarballFiles {
		if n == name {
			return true
		}
	}
	return false
}

// cleanUpdateLeftovers removes the hidden temporary files an update that
// stopped partway (the app was killed) left next to the program. Over an
// hour old only, so a running update isn't touched.
func cleanUpdateLeftovers(exe string) {
	if exe == packagedExe {
		return
	}
	for _, name := range tarballFiles {
		left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "."+name+".update-*"))
		for _, p := range left {
			if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) > time.Hour {
				os.Remove(p)
			}
		}
	}
}
