package systool

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// windowsDir asks Windows itself (not %SystemRoot%) where it is installed.
func windowsDir() string {
	if d, err := windows.GetSystemWindowsDirectory(); err == nil && d != "" {
		return d
	}
	return `C:\Windows`
}

// Find returns where a Windows tool lives: explorer in the Windows folder,
// PowerShell in its own folder, everything else in System32.
func Find(name string) (string, bool) {
	var p string
	switch name {
	case "explorer":
		p = filepath.Join(windowsDir(), "explorer.exe")
	case "powershell":
		p = filepath.Join(windowsDir(), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	default:
		p = filepath.Join(windowsDir(), "System32", name+".exe")
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", false
	}
	return p, true
}

// ProgramFiles is the Program Files folder, asked from Windows (not the
// %ProgramFiles% variable, which any parent process can set).
func ProgramFiles() string {
	if d, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0); err == nil && d != "" {
		return d
	}
	return `C:\Program Files`
}
