package main

import (
	"path/filepath"
	"testing"
)

func TestExistingAncestor(t *testing.T) {
	root := t.TempDir()
	if got := existingAncestor(root); got != root {
		t.Errorf("existing dir: got %q", got)
	}
	missing := filepath.Join(root, "Torrents", "deeper")
	if got := existingAncestor(missing); got != root {
		t.Errorf("deleted folder: got %q, want %q", got, root)
	}
	if got := existingAncestor(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

// A magnet from outside the app is only remembered: no engine is involved
// (so no tracker, DHT or peer is contacted) until the user starts it.
func TestExternalMagnetWaits(t *testing.T) {
	a := &App{configDir: t.TempDir()}
	a.settings.DownloadDir = t.TempDir()
	const ih = "0123456789abcdef0123456789abcdef01234567"
	link := "magnet:?xt=urn:btih:" + ih + "&dn=Some+Linux.iso&tr=https://tracker.example/announce"
	if err := a.addSourceFrom(link, true); err != nil {
		t.Fatal(err)
	}
	if a.eng != nil {
		t.Fatal("an engine was started")
	}
	list := a.ListTorrents()
	if len(list) != 1 || list[0].Hash != ih || list[0].Name != "Some Linux.iso" || !list[0].Confirm {
		t.Fatalf("list: %+v", list)
	}
	if fs, err := a.GetFiles(ih); err != nil || len(fs) != 0 {
		t.Fatalf("files: %v %v", fs, err)
	}
	if err := a.Remove(ih, true); err != nil {
		t.Fatal(err)
	}
	if len(a.ListTorrents()) != 0 {
		t.Fatal("still listed after Remove")
	}
}
