package main

import (
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"os"
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

// An outside .torrent shows its file-list warnings while it waits, before
// anything is downloaded.
func TestExternalTorrentWarnings(t *testing.T) {
	a := &App{configDir: t.TempDir()}
	a.settings.DownloadDir = t.TempDir()
	info := metainfo.Info{Name: "Movie", PieceLength: 16 << 10, Pieces: make([]byte, 20), Files: []metainfo.FileInfo{
		{Path: []string{"movie.mkv"}, Length: 1 << 30},
		{Path: []string{"movie.mp4.exe"}, Length: 1000},
	}}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "movie.torrent")
	f, _ := os.Create(src)
	(&metainfo.MetaInfo{InfoBytes: ib}).Write(f)
	f.Close()
	if err := a.addSourceFrom(src, true); err != nil {
		t.Fatal(err)
	}
	list := a.ListTorrents()
	if len(list) != 1 || !list[0].Confirm || len(list[0].Warnings) == 0 {
		t.Fatalf("list: %+v", list)
	}
	// A file that isn't a valid .torrent is refused, not saved blank.
	bad := filepath.Join(t.TempDir(), "bad.torrent")
	os.WriteFile(bad, []byte("d4:infoi1ee"), 0o644)
	if err := a.addSourceFrom(bad, true); err == nil {
		t.Error("broken .torrent accepted")
	}
}
