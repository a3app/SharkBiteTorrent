package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// Handles opened before the download folder went away and came back, and
// handles that failed a write, are closed instead of reused: on a network
// drive they belong to a dropped connection, and reusing them made every
// write fail while the torrent re-downloaded the same pieces.
func TestStaleHandlesNotReused(t *testing.T) {
	dir := t.TempDir()
	info := &metainfo.Info{Name: "f.bin", PieceLength: 16 << 10, Length: 32 << 10, Pieces: make([]byte, 40)}
	st := newFileStore(dir, storage.NewMapPieceCompletion(), "", false)
	impl, err := st.OpenTorrent(t.Context(), info, hashN(5))
	if err != nil {
		t.Fatal(err)
	}
	defer impl.Close()
	ft := st.ft

	idle, err := ft.acquire(0, true)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := ft.acquire(0, true)
	if err != nil {
		t.Fatal(err)
	}
	ft.release(0, idle) // idle in the pool; busy still in use

	st.reclaim() // the folder came back
	if n := len(ft.handles[0].idle); n != 0 {
		t.Errorf("%d old handles still idle after the drive came back", n)
	}
	if _, err := idle.Stat(); err == nil {
		t.Error("an old idle handle wasn't closed")
	}
	ft.release(0, busy) // in use during reclaim: closed when given back
	if _, err := busy.Stat(); err == nil {
		t.Error("an old handle in use wasn't closed when given back")
	}
	if n := ft.handles[0].open; n != 0 {
		t.Errorf("open count %d, want 0", n)
	}

	// A handle that failed is closed too; the next one is fresh.
	f, err := ft.acquire(0, true)
	if err != nil {
		t.Fatal(err)
	}
	ft.discard(0, f)
	if _, err := f.Stat(); err == nil {
		t.Error("a failed handle wasn't closed")
	}
	g, err := ft.acquire(0, true)
	if err != nil {
		t.Fatal(err)
	}
	if g == f {
		t.Error("a failed handle was reused")
	}
	ft.release(0, g)
	if ft.handles[0].open != 1 || len(ft.handles[0].idle) != 1 {
		t.Errorf("pool: open %d idle %d, want 1 and 1", ft.handles[0].open, len(ft.handles[0].idle))
	}
}
