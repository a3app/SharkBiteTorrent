// Package earlyinit sets environment variables that third-party packages
// read in their own init functions, so they must be set before those run.
//
// Go initializes packages in import-path order once their dependencies are
// ready (spec: "Package initialization"). This module's path, a.local/...,
// sorts before github.com/..., and it only depends on os, so its init runs
// before github.com/anacrolix/torrent/storage's. Import it blank from main.
package earlyinit

import "os"

func init() {
	// anacrolix/torrent's default file storage memory-maps files and never
	// unmaps them, even after a torrent is dropped. On Windows that keeps the
	// files locked, so "remove and delete files" fails until the app exits.
	// Plain reads/writes release files as soon as each operation finishes.
	if _, ok := os.LookupEnv("TORRENT_STORAGE_DEFAULT_FILE_IO"); !ok {
		os.Setenv("TORRENT_STORAGE_DEFAULT_FILE_IO", "classic")
	}
}
