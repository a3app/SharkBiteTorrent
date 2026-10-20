//go:build linux || darwin

package platform

import (
	"os"
	"path/filepath"
)

// inWorldWritableDir reports whether p is inside a directory any user can
// write to (like /tmp), where someone else could later put their own file at
// that path. RefreshStartup won't point sign-in start there. A var so tests
// (whose temp folders are under /tmp) can relax it.
var inWorldWritableDir = func(p string) bool {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && fi.Mode().Perm()&0o002 != 0 {
			return true
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}
