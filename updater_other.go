//go:build !windows

package main

import "errors"

// On Linux the package manager (pacman) owns the install, and on macOS the
// app is unsigned, so updates are offered as a link to the release page.

func canSelfUpdate(string) bool { return false }

func runInstaller(string) error { return errors.New("not supported on this platform") }
