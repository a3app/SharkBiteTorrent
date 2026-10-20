package platform

import "testing"

func autostartFile() string { return autostartPath() }

func TestDesktopExec(t *testing.T) {
	if got, err := desktopExec("/home/a/Shark & Bite/app"); err != nil || got != `"/home/a/Shark & Bite/app"` {
		t.Errorf("plain path: %q, %v", got, err)
	}
	for _, p := range []string{`/a/"b`, "/a/`b", "/a/$b", `/a/\b`, "/a/100%/b", "/a/b\nX-Evil=1"} {
		if _, err := desktopExec(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}
