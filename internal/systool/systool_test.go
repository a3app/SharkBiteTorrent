package systool

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestFind(t *testing.T) {
	name := "ls"
	if runtime.GOOS == "windows" {
		name = "arp"
	}
	p, ok := Find(name)
	if !ok || !filepath.IsAbs(p) {
		t.Fatalf("Find(%q) = %q, %v", name, p, ok)
	}
	if _, ok := Find("no-such-tool-sharkbite"); ok {
		t.Error("found a tool that doesn't exist")
	}
	t.Setenv("PATH", t.TempDir()) // PATH plays no part
	if q, ok := Find(name); !ok || q != p {
		t.Errorf("PATH changed the result: %q", q)
	}
	if err := Command("no-such-tool-sharkbite").Run(); err == nil {
		t.Error("missing tool ran")
	}
}
