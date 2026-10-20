package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func multiInfo(name string, files ...[]string) *metainfo.Info {
	info := &metainfo.Info{Name: name, PieceLength: 16 << 10, Pieces: make([]byte, 20)}
	for _, f := range files {
		info.Files = append(info.Files, metainfo.FileInfo{Path: f, Length: 1})
	}
	return info
}

func TestTorrentPathsRejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	bad := []*metainfo.Info{
		{Name: "", Length: 1},
		{Name: ".", Length: 1},
		{Name: "..", Length: 1},
		{Name: ".bashrc", Length: 1},    // hidden top-level name: the user's own dotfiles
		{Name: "a:b", Length: 1},        // NTFS alternate data stream
		{Name: "movie.exe.", Length: 1}, // Windows drops the trailing dot
		{Name: "movie.exe ", Length: 1},
		{Name: "a\nb", Length: 1},
		multiInfo(".ssh", []string{"authorized_keys"}),
		multiInfo("ok", []string{"..", "x"}),
		multiInfo("ok", []string{"sub", "x:y"}),
		multiInfo("ok", []string{""}),
	}
	if runtime.GOOS == "windows" {
		bad = append(bad, &metainfo.Info{Name: "CON", Length: 1}, multiInfo("ok", []string{"nul"}))
	}
	for _, info := range bad {
		if p, err := torrentPaths(dir, info); err == nil {
			t.Errorf("%q %v accepted: %v", info.Name, info.Files, p)
		}
	}
	good := multiInfo("Show S01", []string{"Extras", ".nfo-folder", "a.mkv"}, []string{"b.mkv"})
	p, err := torrentPaths(dir, good)
	if err != nil || len(p) != 2 || p[1] != filepath.Join(dir, "Show S01", "b.mkv") {
		t.Errorf("good torrent: %v, %v", p, err)
	}
}

// writePiece writes and completes piece 0 through the store, which creates
// the torrent's first file.
func writePiece(t *testing.T, impl storage.TorrentImpl, info *metainfo.Info) error {
	t.Helper()
	pc := impl.Piece(info.Piece(0))
	if _, err := pc.WriteAt([]byte("x"), 0); err != nil {
		return err
	}
	return pc.MarkComplete()
}

func hashN(n byte) (h metainfo.Hash) { h[0] = n; return }

// A torrent is blocked if its paths are taken when it's claimed, stays
// blocked across restarts, and isn't blocked on its own files.
func TestClaim(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	pc := storage.NewMapPieceCompletion()
	open := func(info *metainfo.Info, ih metainfo.Hash, d string, adopt bool) (*fileStore, storage.TorrentImpl) {
		t.Helper()
		st := newFileStore(d, pc, owned, adopt)
		impl, err := st.OpenTorrent(t.Context(), info, ih)
		if err != nil {
			t.Fatal(err)
		}
		return st, impl
	}

	// The user's own file at the torrent's path: blocked, untouched, and
	// still blocked after a restart (the audit's reproduced case).
	os.MkdirAll(filepath.Join(dir, "Music"), 0o755)
	os.WriteFile(filepath.Join(dir, "Music", "song.mp3"), []byte("user's own"), 0o644)
	music := multiInfo("Music", []string{"song.mp3"})
	st, impl := open(music, hashN(1), dir, false)
	if st.Blocked() == nil {
		t.Fatal("not blocked over an existing file")
	}
	writePiece(t, impl, music)
	if st, _ = open(music, hashN(1), dir, false); st.Blocked() == nil {
		t.Error("blocked torrent unblocked by a restart")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Music", "song.mp3")); string(b) != "user's own" {
		t.Errorf("existing file changed: %q", b)
	}
	if len(st.files()) != 0 {
		t.Error("a blocked torrent lists files as its own")
	}

	// A torrent that passed isn't blocked on its own files after a restart.
	iso := multiInfo("Linux", []string{"a.iso"})
	st, impl = open(iso, hashN(2), dir, false)
	if st.Blocked() != nil || writePiece(t, impl, iso) != nil {
		t.Fatalf("new torrent: %v", st.Blocked())
	}
	if st, _ = open(iso, hashN(2), dir, false); st.Blocked() != nil {
		t.Fatalf("own file refused after restart: %v", st.Blocked())
	}

	// In another folder, existing files there aren't its own.
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "Linux"), 0o755)
	os.WriteFile(filepath.Join(other, "Linux", "a.iso"), []byte("someone else's"), 0o644)
	if st, _ := open(iso, hashN(2), other, false); st.Blocked() == nil {
		t.Error("files in another folder taken as the torrent's own")
	}

	// Saved by an older version (no marker): its files are adopted.
	legacy := multiInfo("Old", []string{"old.mkv"})
	os.MkdirAll(filepath.Join(dir, "Old"), 0o755)
	os.WriteFile(filepath.Join(dir, "Old", "old.mkv"), []byte("1"), 0o644)
	if st, _ := open(legacy, hashN(7), dir, false); st.Blocked() == nil {
		t.Error("files adopted for a torrent not from the saved list")
	}
	os.Remove(markerPath(owned, hashN(7), dir))
	if st, _ := open(legacy, hashN(7), dir, true); st.Blocked() != nil || len(st.files()) != 1 {
		t.Errorf("older version's torrent not adopted: %v %v", st.Blocked(), st.files())
	}
}

// Remove never removes a symlink (or junction) the user made, even when it
// has the torrent's folder name.
func TestRemoveKeepsLinks(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "Music")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	f := filepath.Join(dir, "Music", "song.mp3")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := removeFiles(dir, []string{f}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "Music")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the user's link was removed: %v", err)
	}
}

// Remove with files deletes the torrent's own files and its empty folders,
// never other files in a shared folder, and never the download folder.
func TestRemoveFilesOnlyOwn(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	own := []string{mk("Season 1/e01.mkv"), mk("Season 1/Subs/e01.srt")}
	other := mk("Season 1/e02.mkv") // another torrent's file in the same folder
	mk("Season 1.part")             // unrelated
	if err := removeFiles(dir, own); err != nil {
		t.Fatal(err)
	}
	for _, p := range own {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s not deleted", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Season 1", "Subs")); !os.IsNotExist(err) {
		t.Error("empty folder left behind")
	}
	for _, p := range []string{other, filepath.Join(dir, "Season 1.part"), dir} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted", p)
		}
	}
}
