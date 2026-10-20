package platform

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows starts programs listed under HKCU\…\Run when the user signs in (no
// admin needed). Task Manager's Startup tab can switch an entry off without
// deleting it; that's recorded under StartupApproved\Run.
const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
)

var runValue = "SharkBiteTorrent" // a var so tests can use their own

func startupCommand(exe string) string { return `"` + exe + `" ` + AutostartArg }

// StartupEnabled reports whether the app starts at sign-in.
func StartupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_, _, err = k.GetStringValue(runValue)
	k.Close()
	if err != nil {
		return false
	}
	// Switched off in Task Manager: the first byte is odd (1 or 3).
	if a, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.QUERY_VALUE); err == nil {
		b, _, err := a.GetBinaryValue(runValue)
		a.Close()
		if err == nil && len(b) > 0 && b[0]&1 == 1 {
			return false
		}
	}
	return true
}

// SetStartup adds or removes the sign-in entry for exe.
func SetStartup(exe string, on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Turning it on also clears a Task Manager "Disabled", which would
	// otherwise keep it off.
	if a, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE); err == nil {
		a.DeleteValue(runValue)
		a.Close()
	}
	if on {
		return k.SetStringValue(runValue, startupCommand(exe))
	}
	if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// RefreshStartup points an existing entry at exe if the program it names is
// gone (the app was moved or reinstalled elsewhere). An entry for another
// copy that still exists is left alone.
func RefreshStartup(exe string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue(runValue)
	if err != nil {
		return
	}
	old := strings.TrimPrefix(cmd, `"`)
	if i := strings.Index(old, `"`); i >= 0 {
		old = old[:i]
	}
	if _, err := os.Stat(old); err != nil {
		k.SetStringValue(runValue, startupCommand(exe))
	}
}
