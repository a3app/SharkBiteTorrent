//go:build !linux

package platform

// UnmountedDrive is Linux-only: elsewhere a drive that isn't connected
// leaves no folder behind (macOS's /Volumes/X goes away), which the caller
// already catches.
func UnmountedDrive(string) string { return "" }
