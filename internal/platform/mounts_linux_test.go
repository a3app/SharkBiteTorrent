package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/mnt/my\040nas`); got != "/mnt/my nas" {
		t.Errorf("got %q", got)
	}
	if got := unescapeMount(`/mnt/back\slash`); got != `/mnt/back\slash` {
		t.Errorf("got %q", got)
	}
}

// A link to a mount point is resolved; the missing rest is kept as written.
func TestResolveExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mnt", "nas")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Media")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	real, _ := filepath.EvalSymlinks(target)
	if got, want := resolveExisting(filepath.Join(link, "Torrents", "new")), filepath.Join(real, "Torrents", "new"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := resolveExisting("/no/such/path/at/all"); got != "/no/such/path/at/all" {
		t.Errorf("missing path changed: %q", got)
	}
}
