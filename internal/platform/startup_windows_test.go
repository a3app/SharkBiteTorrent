package platform

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestStartupWindows(t *testing.T) {
	// Writing a sign-in (Run) entry from a test binary looks like malware
	// adding itself to startup: Microsoft Defender's behaviour detection
	// (Behavior:Win32/Persistence.A!ml) quarantined go.exe on a
	// developer's PC over it. CI runs it; elsewhere, opt in.
	if os.Getenv("CI") == "" && os.Getenv("SBT_REGISTRY_TESTS") == "" {
		t.Skip("writes a Run entry, which antivirus can flag; set SBT_REGISTRY_TESTS=1 to run it")
	}
	runValue = "SharkBiteTorrentTest"
	t.Cleanup(func() {
		SetStartup("", false)
		runValue = "SharkBiteTorrent"
	})
	exe := filepath.Join(t.TempDir(), "app.exe")
	os.WriteFile(exe, nil, 0o644)

	if StartupEnabled() {
		t.Fatal("enabled before turning it on")
	}
	if err := SetStartup(exe, true); err != nil {
		t.Fatal(err)
	}
	if !StartupEnabled() {
		t.Fatal("not enabled after turning it on")
	}
	k, _ := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	cmd, _, _ := k.GetStringValue(runValue)
	k.Close()
	if cmd != `"`+exe+`" --autostart` {
		t.Errorf("command %q", cmd)
	}

	// Switched off in Task Manager → off; turning it on again clears that.
	a, _, _ := registry.CreateKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE)
	a.SetBinaryValue(runValue, []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	a.Close()
	if StartupEnabled() {
		t.Error("Task Manager's Disabled ignored")
	}
	SetStartup(exe, true)
	if !StartupEnabled() {
		t.Error("turning it on didn't clear Task Manager's Disabled")
	}

	// The app moved: the entry follows it. Another copy that exists: left alone.
	other := filepath.Join(t.TempDir(), "other.exe")
	os.WriteFile(other, nil, 0o644)
	RefreshStartup(other)
	k, _ = registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	cmd, _, _ = k.GetStringValue(runValue)
	k.Close()
	if cmd != `"`+exe+`" --autostart` {
		t.Errorf("entry for an existing copy was changed: %q", cmd)
	}
	os.Remove(exe)
	RefreshStartup(other)
	k, _ = registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	cmd, _, _ = k.GetStringValue(runValue)
	k.Close()
	if cmd != `"`+other+`" --autostart` {
		t.Errorf("entry for a removed copy not updated: %q", cmd)
	}

	if err := SetStartup(other, false); err != nil || StartupEnabled() {
		t.Errorf("turning it off: %v", err)
	}
	if err := SetStartup(other, false); err != nil {
		t.Errorf("turning it off twice: %v", err)
	}
}
