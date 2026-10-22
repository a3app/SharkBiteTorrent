//go:build !linux && !darwin

package engine

// On Windows a disconnected drive's folder doesn't exist at all, which
// folderUnavailable already catches.
func mountRoot(string) string { return "" }

func isMountRoot(string) bool { return true }
