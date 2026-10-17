// Package systool runs the operating system's own tools (route, arp,
// explorer, open, xdg-open, …) from their fixed locations, never by
// searching PATH, where a user-writable folder could hold a stand-in.
package systool

import "os/exec"

// Command is exec.Command for a system tool. If the tool isn't installed,
// running the command fails with a "not found" error.
func Command(name string, args ...string) *exec.Cmd {
	p, ok := Find(name)
	if !ok {
		cmd := exec.Command(name, args...)
		cmd.Path, cmd.Err = "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		return cmd
	}
	return exec.Command(p, args...)
}
