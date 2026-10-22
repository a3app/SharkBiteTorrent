//go:build !linux && !darwin

package engine

// fsType isn't needed elsewhere: on Windows a disconnected drive's folder
// doesn't exist at all.
func fsType(string) (uint64, bool) { return 0, false }
