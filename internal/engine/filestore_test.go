package engine

import (
	"bytes"
	"crypto/sha1"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		// Closed before the temporary folder is deleted: Windows can't
		// delete files that are still open.
		t.Cleanup(func() { impl.Close() })
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

	// Saved by an older version (no record): its files are adopted.
	legacy := multiInfo("Old", []string{"old.mkv"})
	os.MkdirAll(filepath.Join(dir, "Old"), 0o755)
	os.WriteFile(filepath.Join(dir, "Old", "old.mkv"), []byte("1"), 0o644)
	if st, _ := open(legacy, hashN(7), dir, false); st.Blocked() == nil {
		t.Error("files adopted for a torrent not from the saved list")
	}
	os.Remove(recordPath(owned, hashN(7), dir))
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
	if err := removeFiles(dir, []string{f}, nil, true); err != nil {
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
	if err := removeFiles(dir, own, nil, true); err != nil {
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

// A new torrent whose folder inside the download folder is a link (to the
// user's library elsewhere) is refused: it would write there.
func TestClaimRefusesLinkedFolder(t *testing.T) {
	dir, owned, library := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Symlink(library, filepath.Join(dir, "Music")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	st := newFileStore(dir, storage.NewMapPieceCompletion(), owned, false)
	impl, err := st.OpenTorrent(t.Context(), multiInfo("Music", []string{"new.mp3"}), hashN(8))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { impl.Close() })
	if st.Blocked() == nil {
		t.Error("torrent allowed to write through a linked folder")
	}
}

// A piece that's complete and correct in memory when the torrent closes is
// written and recorded, not thrown away (on a slow NAS a backlog of them
// built up, and quitting lost them); one that fails its hash isn't.
func TestCloseWritesFinishedPieces(t *testing.T) {
	dir := t.TempDir()
	pc := storage.NewMapPieceCompletion()
	good := bytes.Repeat([]byte("g"), 16<<10)
	sum := sha1.Sum(good)
	info := &metainfo.Info{Name: "f.bin", PieceLength: 16 << 10, Length: 32 << 10, Pieces: append(sum[:], make([]byte, 20)...)}
	st := newFileStore(dir, pc, "", false)
	impl, err := st.OpenTorrent(t.Context(), info, hashN(9))
	if err != nil {
		t.Fatal(err)
	}
	// A first write creates the file; then each piece is only buffered.
	if _, err := impl.Piece(info.Piece(0)).WriteAt(good, 0); err != nil {
		t.Fatal(err)
	}
	impl.Piece(info.Piece(1)).WriteAt(bytes.Repeat([]byte("b"), 16<<10), 0) // wrong data for piece 1's hash
	if err := impl.Close(); err != nil {
		t.Fatal(err)
	}
	c0, _ := pc.Get(metainfo.PieceKey{InfoHash: hashN(9), Index: 0})
	c1, _ := pc.Get(metainfo.PieceKey{InfoHash: hashN(9), Index: 1})
	if !c0.Complete || c1.Complete {
		t.Fatalf("piece 0 recorded %v (want true), piece 1 %v (want false)", c0.Complete, c1.Complete)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin")); !bytes.Equal(b[:16<<10], good) {
		t.Error("piece 0's data wasn't written")
	}
}

// openStore opens info in dir with ownership records in owned, closing it
// when the test ends.
func openStore(t *testing.T, dir, owned string, info *metainfo.Info, ih metainfo.Hash) (*fileStore, storage.TorrentImpl) {
	t.Helper()
	st := newFileStore(dir, storage.NewMapPieceCompletion(), owned, false)
	impl, err := st.OpenTorrent(t.Context(), info, ih)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { impl.Close() })
	return st, impl
}

// A file the torrent created that's since been replaced (deleted, and a new
// one made at its path: the user's, or a reused inode) isn't its own: the
// torrent is blocked over it and never lists it for deleting.
func TestReplacedFileNotOwned(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	info := multiInfo("Linux", []string{"a.iso"})
	st, impl := openStore(t, dir, owned, info, hashN(3))
	if err := writePiece(t, impl, info); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "Linux", "a.iso")
	if f := st.files(); len(f) != 1 || f[0] != p {
		t.Fatalf("own file not listed: %v", f)
	}
	impl.Close()
	os.Remove(p)
	time.Sleep(10 * time.Millisecond) // creation times are at least this fine everywhere tested
	os.WriteFile(p, []byte("user's"), 0o644)
	st, _ = openStore(t, dir, owned, info, hashN(3))
	if st.Blocked() == nil {
		t.Error("replaced file taken as the torrent's own")
	}
	o := loadOwnership(owned, hashN(3), dir, false)
	if len(o.files()) != 0 {
		t.Errorf("replaced file still recorded: %v", o.files())
	}
}

// A file that appears at one of the torrent's paths after it was checked is
// never written into.
func TestNoWriteIntoLaterFile(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	info := multiInfo("Show", []string{"a"}, []string{"b"})
	_, impl := openStore(t, dir, owned, info, hashN(4))
	b := filepath.Join(dir, "Show", "b")
	os.MkdirAll(filepath.Dir(b), 0o755)
	os.WriteFile(b, []byte("user's"), 0o644)
	pc := impl.Piece(info.Piece(0))
	pc.WriteAt([]byte("xy"), 0)
	if pc.MarkComplete() == nil {
		t.Error("wrote into a file it didn't create")
	}
	if got, _ := os.ReadFile(b); string(got) != "user's" {
		t.Errorf("file changed: %q", got)
	}
}

// Records are per folder: the same torrent in another folder starts clean,
// and its files in the first folder stay its own there.
func TestOwnershipPerFolder(t *testing.T) {
	a, b, owned := t.TempDir(), t.TempDir(), t.TempDir()
	info := multiInfo("Linux", []string{"a.iso"})
	_, impl := openStore(t, a, owned, info, hashN(5))
	writePiece(t, impl, info)
	impl.Close()
	st, impl := openStore(t, b, owned, info, hashN(5))
	if st.Blocked() != nil || writePiece(t, impl, info) != nil {
		t.Fatalf("second folder: %v", st.Blocked())
	}
	impl.Close()
	for _, d := range []string{a, b} {
		if st, _ := openStore(t, d, owned, info, hashN(5)); st.Blocked() != nil || len(st.files()) != 1 {
			t.Errorf("%s: %v %v", d, st.Blocked(), st.files())
		}
	}
}

// A file noted as pending (the app stopped between creating and recording
// it) is the torrent's own if it starts with the tag noted, or is empty; not
// if the note was cancelled (the path was taken), or if it has the right
// length but other data (a copy of the same file put there).
func TestPendingRecovered(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	o := loadOwnership(owned, hashN(6), dir, true)
	tag := newTag(100)
	for _, name := range []string{"tagged", "empty", "taken", "copy"} {
		if o.pending(path(name), tag) != nil {
			t.Fatal("pending")
		}
	}
	o.cancel(path("taken"))
	tagged := make([]byte, 100)
	copy(tagged, tag)
	os.WriteFile(path("tagged"), tagged, 0o644)
	os.WriteFile(path("empty"), nil, 0o644)
	os.WriteFile(path("taken"), tagged, 0o644)
	os.WriteFile(path("copy"), bytes.Repeat([]byte("x"), 100), 0o644)
	o = loadOwnership(owned, hashN(6), dir, true)
	for name, want := range map[string]bool{"tagged": true, "empty": true, "taken": false, "copy": false} {
		if o.has(path(name)) != want {
			t.Errorf("%s: owned %v", name, !want)
		}
	}
	// Recorded for good once loaded; the notes are gone.
	if o = loadOwnership(owned, hashN(6), dir, true); !o.has(path("tagged")) {
		t.Error("recovered file forgotten")
	}
	if b, _ := os.ReadFile(o.path); bytes.Contains(b, []byte("p\t")) {
		t.Errorf("pending notes kept:\n%s", b)
	}
}

// A file the torrent made, deleted, and made again (the app stopping before
// recording the new one) is recovered: the old file's entry, before the
// note, doesn't settle it.
func TestPendingAfterOldEntry(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "a.iso")
	os.WriteFile(p, []byte("old"), 0o644)
	o := loadOwnership(owned, hashN(13), dir, true)
	if err := o.add(p); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	time.Sleep(10 * time.Millisecond)
	tag := newTag(50)
	o.pending(p, tag)
	os.WriteFile(p, append(tag, make([]byte, 34)...), 0o644)
	if !loadOwnership(owned, hashN(13), dir, true).has(p) {
		t.Error("re-created file not recovered")
	}
}

// The store's new files start with the tag noted before creating them, at
// their full length, and are then recorded.
func TestNewFileTagged(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	info := multiInfo("T", []string{"a"})
	info.Files[0].Length = 40
	st, _ := openStore(t, dir, owned, info, hashN(14))
	f, err := st.ft.createFile(0)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	p := filepath.Join(dir, "T", "a")
	b, _ := os.ReadFile(p)
	rec, _ := os.ReadFile(recordPath(owned, hashN(14), dir))
	var tag string
	for _, line := range strings.Split(string(rec), "\n") {
		if strings.HasPrefix(line, "p\t") {
			tag = strings.Split(line, "\t")[1]
		}
	}
	if len(b) != 40 || bytes.Equal(b[:16], make([]byte, 16)) || !hasTag(p, tag) {
		t.Errorf("file %x, note %q", b, tag)
	}
	if !st.ownership().has(p) {
		t.Error("not recorded")
	}
}

// A file two torrents' records both claim (v0.2.36 let two torrents share
// one) is reported, so removing one doesn't delete the other's file.
func TestOwnedByOthers(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "same.iso")
	os.WriteFile(p, []byte("1"), 0o644)
	for _, ih := range []metainfo.Hash{hashN(9), hashN(10)} {
		o := loadOwnership(owned, ih, dir, true)
		if err := o.adopt([]string{p}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !ownedByOthers(owned, hashN(9), []string{p})[p] {
		t.Error("shared file not reported")
	}
	loadOwnership(owned, hashN(10), dir, true).forget()
	if ownedByOthers(owned, hashN(9), []string{p})[p] {
		t.Error("file reported shared after the other torrent was removed")
	}
}

// Removing deletes the folders the torrent created, not ones that were
// already there (even empty ones named like it).
func TestRemovePrunesOnlyCreatedFolders(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Show"), 0o755) // the user's, empty
	info := multiInfo("Show", []string{"Season 1", "e01.mkv"})
	st, impl := openStore(t, dir, owned, info, hashN(11))
	if err := writePiece(t, impl, info); err != nil {
		t.Fatal(err)
	}
	impl.Close()
	o := st.ownership()
	if err := removeFiles(dir, o.files(), o.folders(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Show", "Season 1")); !os.IsNotExist(err) {
		t.Error("created folder left behind")
	}
	if _, err := os.Stat(filepath.Join(dir, "Show")); err != nil {
		t.Error("the user's folder was removed")
	}
}

// A torrent that passed v0.2.37's check (an empty marker at the record's
// path) keeps its files after upgrading; one without a marker doesn't get
// them.
func TestV0237MarkerAdopted(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "Linux", "a.iso")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("x"), 0o644)
	info := multiInfo("Linux", []string{"a.iso"})
	if st, _ := openStore(t, dir, owned, info, hashN(12)); st.Blocked() == nil {
		t.Fatal("existing file taken without a marker")
	}
	os.Remove(recordPath(owned, hashN(12), dir))
	os.WriteFile(recordPath(owned, hashN(12), dir), nil, 0o600)
	st, _ := openStore(t, dir, owned, info, hashN(12))
	if st.Blocked() != nil || len(st.files()) != 1 {
		t.Fatalf("v0.2.37 torrent not adopted: %v %v", st.Blocked(), st.files())
	}
	if o := loadOwnership(owned, hashN(12), dir, false); !o.written || !o.has(p) {
		t.Error("adoption not recorded")
	}
}

// The same folder written another way (a link, as D:\dl and d:\dl or a
// share's drive letter and \\server path are on Windows) still counts as
// sharing the file.
func TestOwnedByOthersOtherSpelling(t *testing.T) {
	dir, owned := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	p := filepath.Join(dir, "same.iso")
	os.WriteFile(p, []byte("1"), 0o644)
	if err := loadOwnership(owned, hashN(15), alias, true).adopt([]string{filepath.Join(alias, "same.iso")}, nil); err != nil {
		t.Fatal(err)
	}
	if !ownedByOthers(owned, hashN(16), []string{p})[p] {
		t.Error("file shared under another spelling of the folder not reported")
	}
}
