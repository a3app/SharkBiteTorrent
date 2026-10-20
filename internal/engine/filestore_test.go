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

// The store never writes into files that already exist unless they're its own.
func TestClaimRefusesExistingFiles(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	pc := storage.NewMapPieceCompletion()
	info := multiInfo("Music", []string{"song.mp3"})
	os.MkdirAll(filepath.Join(dir, "Music"), 0o755)
	os.WriteFile(filepath.Join(dir, "Music", "song.mp3"), []byte("user's own"), 0o644)
	var ih metainfo.Hash
	ih[0] = 1

	st := newFileStore(dir, pc, owned)
	impl, err := st.OpenTorrent(t.Context(), info, ih)
	if err != nil {
		t.Fatal(err)
	}
	if st.Blocked() == nil {
		t.Fatal("not blocked over an existing file")
	}
	// Writing is refused and the file is untouched.
	piece := impl.Piece(info.Piece(0))
	piece.WriteAt([]byte("x"), 0)
	piece.MarkComplete()
	if b, _ := os.ReadFile(filepath.Join(dir, "Music", "song.mp3")); string(b) != "user's own" {
		t.Errorf("existing file changed: %q", b)
	}

	// A torrent whose files don't exist yet is allowed and marked as ours,
	// so it can open its own files again after a restart.
	var ih2 metainfo.Hash
	ih2[0] = 2
	st2 := newFileStore(dir, pc, owned)
	info2 := multiInfo("New", []string{"a"})
	if _, err := st2.OpenTorrent(t.Context(), info2, ih2); err != nil || st2.Blocked() != nil {
		t.Fatalf("new torrent blocked: %v %v", err, st2.Blocked())
	}
	os.MkdirAll(filepath.Join(dir, "New"), 0o755)
	os.WriteFile(filepath.Join(dir, "New", "a"), []byte("1"), 0o644)
	st3 := newFileStore(dir, pc, owned)
	if _, err := st3.OpenTorrent(t.Context(), info2, ih2); err != nil || st3.Blocked() != nil {
		t.Fatalf("own files refused after restart: %v %v", err, st3.Blocked())
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
