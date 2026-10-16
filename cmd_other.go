//go:build !windows

package main

import "os/exec"

// hiddenCommand runs a console tool (no window to hide outside Windows).
func hiddenCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
