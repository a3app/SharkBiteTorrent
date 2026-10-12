//go:build !windows

package engine

import "os"

// makeSparse is a no-op: on Linux and macOS filesystems, extending a file
// with truncate already leaves the gap unallocated.
func makeSparse(*os.File) error { return nil }
