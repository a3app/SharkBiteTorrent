//go:build linux || darwin

package engine

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// mountRoot is the mount point dir is on: the folder at or above it whose
// parent is on another device ("/" for the root filesystem).
func mountRoot(dir string) string {
	p := filepath.Clean(dir)
	dev, ok := devOf(p)
	if !ok {
		return ""
	}
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		if pd, ok := devOf(parent); !ok || pd != dev {
			return p
		}
		p = parent
	}
}

// isMountRoot reports whether something is still mounted at p.
func isMountRoot(p string) bool {
	parent := filepath.Dir(p)
	if parent == p {
		return true
	}
	d, ok := devOf(p)
	pd, pok := devOf(parent)
	return ok && pok && d != pd
}

func devOf(p string) (uint64, bool) {
	var st unix.Stat_t
	if unix.Stat(p, &st) != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}
