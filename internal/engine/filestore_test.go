package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// The store only writes into files it created, per torrent and folder.
func TestOwnership(t *testing.T) {
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

	// The user's own file at the torrent's path: blocked, untouched.
	os.MkdirAll(filepath.Join(dir, "Music"), 0o755)
	os.WriteFile(filepath.Join(dir, "Music", "song.mp3"), []byte("user's own"), 0o644)
	music := multiInfo("Music", []string{"song.mp3"})
	st, impl := open(music, hashN(1), dir, false)
	if st.Blocked() == nil {
		t.Fatal("not blocked over an existing file")
	}
	writePiece(t, impl, music)
	if b, _ := os.ReadFile(filepath.Join(dir, "Music", "song.mp3")); string(b) != "user's own" {
		t.Errorf("existing file changed: %q", b)
	}

	// A file the store created is its own after a restart.
	iso := multiInfo("Linux", []string{"a.iso"})
	st, impl = open(iso, hashN(2), dir, false)
	if st.Blocked() != nil || writePiece(t, impl, iso) != nil {
		t.Fatalf("new torrent: %v", st.Blocked())
	}
	if st, _ = open(iso, hashN(2), dir, false); st.Blocked() != nil {
		t.Fatalf("own file refused after restart: %v", st.Blocked())
	}

	// Its record outlives the file: the user deletes it, and another file
	// appears at the same path. The record doesn't vouch for it.
	p := filepath.Join(dir, "Linux", "a.iso")
	os.Remove(p)
	os.WriteFile(p, []byte("a different file"), 0o644)
	if st, _ = open(iso, hashN(2), dir, false); st.Blocked() == nil {
		t.Error("stale record vouched for a different file at the same path")
	}

	// Another torrent with the same name, claimed before the first one wrote
	// anything: the first one's file isn't the second's to write into.
	same := multiInfo("Shared", []string{"x.iso"})
	_, implA := open(same, hashN(3), dir, false)
	stB, implB := open(same, hashN(4), dir, false)
	if err := writePiece(t, implA, same); err != nil {
		t.Fatal(err)
	}
	if err := writePiece(t, implB, same); err == nil || stB.Blocked() == nil {
		t.Errorf("second same-name torrent wrote into the first's file: %v, %v", err, stB.Blocked())
	}

	// The same torrent in two folders keeps a record for each.
	other := t.TempDir()
	stO, implO := open(iso, hashN(6), other, false)
	if stO.Blocked() != nil || writePiece(t, implO, iso) != nil {
		t.Fatal("torrent in a second folder")
	}
	os.Remove(p) // the earlier step's file
	_, impl1 := open(iso, hashN(6), dir, false)
	if err := writePiece(t, impl1, iso); err != nil {
		t.Fatal(err)
	}
	if st, _ := open(iso, hashN(6), other, false); st.Blocked() != nil {
		t.Error("record for the first folder lost when used in another")
	}

	// Upgrading: a torrent from the saved list, with no record, adopts its
	// files; the same files for a torrent that isn't saved are refused.
	legacy := multiInfo("Old", []string{"old.mkv"})
	os.MkdirAll(filepath.Join(dir, "Old"), 0o755)
	os.WriteFile(filepath.Join(dir, "Old", "old.mkv"), []byte("1"), 0o644)
	if st, _ := open(legacy, hashN(7), dir, false); st.Blocked() == nil {
		t.Error("files adopted without the saved list")
	}
	if st, _ := open(legacy, hashN(7), dir, true); st.Blocked() != nil || len(st.ownedFiles()) != 1 {
		t.Errorf("saved torrent's files not adopted: %v %v", st.Blocked(), st.ownedFiles())
	}
}

// A damaged or edited record can't point outside the folder, and a line cut
// off by a crash is ignored.
func TestOwnershipRecordSafety(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(outside, []byte("x"), 0o644)
	id, size, _ := fileID(outside)
	rel, _ := filepath.Rel(dir, outside)
	rec := ownershipHeader + dir + "\n" + id + "\t" + strconv.FormatInt(size, 10) + "\t" + filepath.ToSlash(rel) + "\n" + "abc\t1\tcut"
	os.WriteFile(recordPath(owned, hashN(9), dir), []byte(rec), 0o600)
	if got := loadOwnership(owned, hashN(9), dir).list(); len(got) != 0 {
		t.Errorf("record entries accepted: %v", got)
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
