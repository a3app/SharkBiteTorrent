package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/segments"
	"github.com/anacrolix/torrent/storage"

	"sharkbite/internal/scan"
)

// fileStore is torrent storage tuned for slow or network disks (e.g. a NAS
// share), where anacrolix's default file storage managed ~0.5 MB/s:
//
//   - Files stay open while the torrent is active, instead of an open/close
//     per 16 KiB block (each one a network round trip on SMB).
//   - New files are sparse and pre-sized, so writing past the end doesn't
//     make the filesystem zero-fill the gap first.
//   - Blocks are gathered in memory per piece and written in one go once the
//     piece passes its hash check. On a NAS, 16 KiB random writes ran at
//     ~2 MB/s; piece-sized writes at ~17 MB/s.
//   - Piece completion is persisted (shared database in the app's data
//     folder), so restarts don't re-hash or re-download anything.
//
// Files are written at their final names. Handles are closed and unfinished
// buffers dropped when the torrent is dropped, so files can be deleted after.
//
// The store only writes into and deletes files it created for this torrent
// in this folder (see ownership.go); a torrent whose paths are taken by
// other files is blocked instead.
type fileStore struct {
	dir      string // download folder for this torrent
	pc       storage.PieceCompletion
	ownedDir string // ownership records (see ownership.go); "" skips the check
	adopt    bool   // a torrent from the saved list: adopt its files if it has no record yet

	mu      sync.Mutex
	blocked error        // set if the torrent mustn't write (shown in the list)
	ft      *fileTorrent // the torrent's storage, once opened
	own     *ownership   // nil without ownedDir
}

// ownership returns the torrent's record, nil without one.
func (s *fileStore) ownership() *ownership {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.own
}

// ownedFiles are the files this torrent created here that still exist.
func (s *fileStore) ownedFiles() []string {
	o := s.ownership()
	if o == nil {
		return nil
	}
	var out []string
	for _, p := range o.list() {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// forgetOwnership deletes the record (all of its files were deleted).
func (s *fileStore) forgetOwnership() {
	if o := s.ownership(); o != nil {
		o.forget()
	}
}

func newFileStore(dir string, pc storage.PieceCompletion, ownedDir string, adopt bool) *fileStore {
	return &fileStore{dir: dir, pc: pc, ownedDir: ownedDir, adopt: adopt}
}

// Blocked reports why the torrent may not write its files, if it may not.
func (s *fileStore) Blocked() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocked
}

func (s *fileStore) block(err error) {
	s.mu.Lock()
	s.blocked = err
	s.mu.Unlock()
}

// torrentPaths returns where each of the torrent's files goes under dir.
// It refuses names that could leave dir, hide among the user's own files
// (a top-level ".ssh" or ".bashrc" in a home folder), name the download
// folder itself ("" or "."), or mean something special to the OS (Windows
// device names like CON, ":" streams, trailing dots and spaces).
func torrentPaths(dir string, info *metainfo.Info) ([]string, error) {
	name := info.BestName()
	if !safeName(name) || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("the torrent's name %q isn't safe to use as a file name", name)
	}
	var out []string
	for _, fi := range info.UpvertedFiles() {
		parts := []string{dir, name}
		if info.IsDir() {
			for _, c := range fi.BestPath() {
				if !safeName(c) {
					return nil, fmt.Errorf("the torrent has a file name that isn't safe: %q", strings.Join(fi.BestPath(), "/"))
				}
				parts = append(parts, c)
			}
		}
		p := filepath.Join(parts...)
		if !isSub(dir, p) || p == filepath.Clean(dir) {
			return nil, fmt.Errorf("unsafe file path in torrent: %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

// safeName reports whether c is usable as one file or folder name.
func safeName(c string) bool {
	return c != "" && c != "." && filepath.IsLocal(c) &&
		!strings.ContainsAny(c, `/\:`) && strings.IndexFunc(c, unicode.IsControl) < 0 &&
		!strings.HasSuffix(c, ".") && !strings.HasSuffix(c, " ")
}

const (
	blockSize = 16 << 10 // BitTorrent's request size; buffers track blocks
	// maxBuffered caps memory used by unfinished pieces per torrent. Past
	// it, the oldest partial piece is written to disk early.
	maxBuffered = 256 << 20
)

func (s *fileStore) OpenTorrent(_ context.Context, info *metainfo.Info, ih metainfo.Hash) (storage.TorrentImpl, error) {
	ft := &fileTorrent{
		store:   s,
		info:    info,
		pc:      s.pc,
		ih:      ih,
		index:   info.FileSegmentsIndex(),
		handles: map[int]*handlePool{},
		bufs:    map[int]*pieceBuf{},
		busy:    map[int]int{},
	}
	paths, err := torrentPaths(s.dir, info)
	if err == nil {
		err = s.claim(ih, info, paths)
	}
	if err != nil {
		// Blocked: no file is opened (see acquire), and the engine stops
		// the torrent and shows why.
		s.block(err)
		ft.blocked = err
		paths = make([]string, len(info.UpvertedFiles()))
	}
	ft.paths = paths
	for _, fi := range info.UpvertedFiles() {
		ft.lengths = append(ft.lengths, fi.Length)
	}
	ft.freed = sync.NewCond(&ft.mu)
	s.mu.Lock()
	s.ft = ft
	s.mu.Unlock()
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &filePiece{t: ft, p: p}
		},
		Close: ft.close,
	}, nil
}

func isSub(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type fileTorrent struct {
	store   *fileStore
	info    *metainfo.Info
	pc      storage.PieceCompletion
	ih      metainfo.Hash
	index   segments.Index
	paths   []string
	lengths []int64
	blocked error // see fileStore.claim; no file is opened if set

	mu       sync.Mutex
	freed    *sync.Cond          // signalled when a handle is released
	handles  map[int]*handlePool // open handles, by file index
	bufs     map[int]*pieceBuf   // unfinished pieces, by piece index
	buffered int64
	seq      uint64 // allocation order, to evict the oldest buffer
	closed   bool
	inUse    int         // handles handed out and not yet released, plus MarkCompletes running
	busy     map[int]int // handles in use, by file index
}

// pieceBuf holds a piece's blocks until it's complete. have marks which
// blocks were written into it.
type pieceBuf struct {
	data []byte
	have []bool
	seq  uint64
}

// handlesPerFile is how many handles a file may have open at once. On
// Windows, reads and writes through a single *os.File run one at a time, so
// with one handle every piece write waited for the previous one's network
// round trip to the NAS. Several handles keep several writes in flight.
const handlesPerFile = 8

type handlePool struct {
	idle []*os.File
	open int // handles opened, idle or in use
}

// acquire returns an open handle for file i, waiting if all of the file's
// handles are busy. With create false, a missing file returns
// fs.ErrNotExist instead of being created (reads of data we don't have,
// e.g. skipped files, shouldn't leave empty files behind). A newly created
// file is made sparse and sized to its full length. Call release after use.
func (t *fileTorrent) acquire(i int, create bool) (*os.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.blocked != nil {
		return nil, t.blocked
	}
	for {
		if t.closed {
			return nil, errors.New("torrent storage closed")
		}
		pool := t.handles[i]
		if pool == nil {
			pool = &handlePool{}
			t.handles[i] = pool
		}
		if n := len(pool.idle); n > 0 {
			f := pool.idle[n-1]
			pool.idle = pool.idle[:n-1]
			t.inUse++
			t.busy[i]++
			return f, nil
		}
		if pool.open < handlesPerFile {
			f, err := t.openFile(i, create)
			if err != nil {
				return nil, err
			}
			pool.open++
			t.inUse++
			t.busy[i]++
			return f, nil
		}
		t.freed.Wait()
	}
}

func (t *fileTorrent) release(i int, f *os.File) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inUse--
	if t.busy[i]--; t.busy[i] <= 0 {
		delete(t.busy, i)
	}
	if t.closed {
		f.Close()
		t.freed.Broadcast() // waitReleased
		return
	}
	t.handles[i].idle = append(t.handles[i].idle, f)
	t.freed.Signal()
}

// waitReleased waits (up to timeout) until the closed torrent's last file
// handle is closed and no piece is still being written, and returns the
// files still open after that. Deleting a file that's still open fails on
// Windows and, on an SMB share, leaves a ".__smbfile_silly" file and can
// stall the share.
func (s *fileStore) waitReleased(timeout time.Duration) (stillOpen map[string]bool) {
	s.mu.Lock()
	t := s.ft
	s.mu.Unlock()
	if t == nil {
		return nil
	}
	timedOut := false
	timer := time.AfterFunc(timeout, func() {
		t.mu.Lock()
		timedOut = true
		t.freed.Broadcast()
		t.mu.Unlock()
	})
	defer timer.Stop()
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.inUse > 0 && !timedOut {
		t.freed.Wait()
	}
	if len(t.busy) == 0 {
		return nil
	}
	stillOpen = map[string]bool{}
	for i := range t.busy {
		stillOpen[t.paths[i]] = true
	}
	return stillOpen
}

// openFile opens (or creates) file i. Called with t.mu held.
func (t *fileTorrent) openFile(i int, create bool) (*os.File, error) {
	own := t.store.ownership()
	f, err := os.OpenFile(t.paths[i], os.O_RDWR, 0o644)
	if err == nil && own != nil && !own.has(t.paths[i]) {
		// It appeared after the torrent was claimed (a file ticked later,
		// another torrent with the same name): not ours to write into.
		f.Close()
		rel, _ := filepath.Rel(t.store.dir, t.paths[i])
		err = fmt.Errorf("%q: %w", rel, errNotOurs)
		t.store.block(fmt.Errorf("%q already exists in the download folder, and Shark BiteTorrent only writes into files it created. Move it away, then add the torrent again", rel))
		return nil, err
	}
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := os.MkdirAll(filepath.Dir(t.paths[i]), 0o755); err != nil {
			return nil, err
		}
		f, err = os.OpenFile(t.paths[i], os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			makeSparse(f) // best effort: unsupported filesystems still work, just slower
			err = f.Truncate(t.lengths[i])
		}
		if err == nil && own != nil {
			// Recorded once it has its final size (part of its identity).
			if err = own.add(t.paths[i]); err != nil {
				f.Close()
				os.Remove(t.paths[i])
				return nil, err
			}
		}
		if err == nil {
			// Marked as downloaded from the internet from the start, so a
			// file opened before the torrent finishes is checked by the OS.
			scan.MarkDownloaded(t.paths[i])
		}
	}
	if err != nil {
		if f != nil {
			f.Close()
		}
		return nil, err
	}
	return f, nil
}

func (t *fileTorrent) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.bufs, t.buffered = map[int]*pieceBuf{}, 0 // unfinished pieces are re-downloaded later
	var errs []error
	for i, pool := range t.handles {
		for _, f := range pool.idle {
			errs = append(errs, f.Close())
		}
		delete(t.handles, i) // handles still in use are closed on release
	}
	t.freed.Broadcast()
	return errors.Join(errs...)
}

type filePiece struct {
	t *fileTorrent
	p metainfo.Piece
}

func (p *filePiece) key() metainfo.PieceKey {
	return metainfo.PieceKey{InfoHash: p.t.ih, Index: p.p.Index()}
}

// diskIO runs fn over each file segment covered by [off, off+len(b)) of the piece.
func (p *filePiece) diskIO(b []byte, off int64, create bool,
	fn func(f *os.File, b []byte, off int64) (int, error)) (n int, err error) {
	ext := segments.Extent{Start: p.p.Offset() + off, Length: int64(len(b))}
	for i, e := range p.t.index.LocateIter(ext) {
		if e.Length == 0 {
			continue
		}
		f, err := p.t.acquire(i, create)
		if errors.Is(err, fs.ErrNotExist) {
			return n, io.EOF // data we don't have reads as a short read
		}
		if err != nil {
			return n, err
		}
		m, err := fn(f, b[n:n+int(e.Length)], e.Start)
		p.t.release(i, f)
		n += m
		if err != nil {
			return n, err
		}
	}
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

// ReadAt reads from disk, then overlays any blocks still in the piece's
// buffer (so hashing sees the blocks that haven't been written out yet).
func (p *filePiece) ReadAt(b []byte, off int64) (int, error) {
	p.t.mu.Lock()
	buf := p.t.bufs[p.p.Index()]
	if buf != nil && covered(buf, off, len(b)) {
		n := copy(b, buf.data[off:])
		p.t.mu.Unlock()
		return n, nil
	}
	p.t.mu.Unlock()

	n, err := p.diskIO(b, off, false, (*os.File).ReadAt)
	if buf != nil {
		p.t.mu.Lock()
		if cur := p.t.bufs[p.p.Index()]; cur == buf {
			overlay(buf, b, off)
		}
		p.t.mu.Unlock()
	}
	return n, err
}

// WriteAt buffers a block in memory. Writes that aren't whole, aligned
// blocks go straight to disk.
func (p *filePiece) WriteAt(b []byte, off int64) (int, error) {
	plen := p.p.Length()
	aligned := off%blockSize == 0 && (len(b) == blockSize || off+int64(len(b)) == plen)
	if !aligned {
		return p.diskIO(b, off, true, (*os.File).WriteAt)
	}
	idx := p.p.Index()

	p.t.mu.Lock()
	buf := p.t.bufs[idx]
	var evict *pieceBuf
	var evictIdx int
	if buf == nil {
		p.t.seq++
		buf = &pieceBuf{
			data: make([]byte, plen),
			have: make([]bool, (plen+blockSize-1)/blockSize),
			seq:  p.t.seq,
		}
		p.t.bufs[idx] = buf
		p.t.buffered += plen
		if p.t.buffered > maxBuffered {
			evictIdx, evict = p.t.oldestOtherThan(idx)
			if evict != nil {
				delete(p.t.bufs, evictIdx)
				p.t.buffered -= int64(len(evict.data))
			}
		}
	}
	copy(buf.data[off:], b)
	buf.have[off/blockSize] = true
	p.t.mu.Unlock()

	if evict != nil {
		// Memory cap reached: write the oldest partial piece out now.
		p.flushAt(evictIdx, evict)
	}
	return len(b), nil
}

func (t *fileTorrent) oldestOtherThan(skip int) (int, *pieceBuf) {
	var oi int
	var ob *pieceBuf
	for i, b := range t.bufs {
		if i != skip && (ob == nil || b.seq < ob.seq) {
			oi, ob = i, b
		}
	}
	return oi, ob
}

// flushAt writes buf's blocks for piece idx to disk, as few large writes as
// possible (contiguous runs of blocks). idx may be a different piece than p
// (when evicting the oldest buffer to stay under the memory cap).
func (p *filePiece) flushAt(idx int, buf *pieceBuf) error {
	piece := p
	if idx != p.p.Index() {
		piece = &filePiece{t: p.t, p: p.t.info.Piece(idx)}
	}
	var errs []error
	for i := 0; i < len(buf.have); {
		if !buf.have[i] {
			i++
			continue
		}
		j := i
		for j < len(buf.have) && buf.have[j] {
			j++
		}
		lo := int64(i) * blockSize
		hi := min(int64(j)*blockSize, int64(len(buf.data)))
		if _, err := piece.diskIO(buf.data[lo:hi], lo, true, (*os.File).WriteAt); err != nil {
			errs = append(errs, err)
		}
		i = j
	}
	return errors.Join(errs...)
}

func (p *filePiece) MarkComplete() error {
	idx := p.p.Index()
	p.t.mu.Lock()
	if p.t.closed {
		p.t.mu.Unlock()
		return errors.New("torrent storage closed")
	}
	p.t.inUse++ // waitReleased waits for this
	defer func() {
		p.t.mu.Lock()
		p.t.inUse--
		p.t.freed.Broadcast()
		p.t.mu.Unlock()
	}()
	buf := p.t.bufs[idx]
	if buf != nil {
		delete(p.t.bufs, idx)
		p.t.buffered -= int64(len(buf.data))
	}
	p.t.mu.Unlock()
	if buf != nil {
		if err := p.flushAt(idx, buf); err != nil {
			return err // not marked complete; the library will retry the piece
		}
	}
	p.t.mu.Lock()
	closed := p.t.closed
	p.t.mu.Unlock()
	if closed {
		// Removed meanwhile: don't leave a "complete" record behind.
		return errors.New("torrent storage closed")
	}
	return p.t.pc.Set(p.key(), true)
}

func (p *filePiece) MarkNotComplete() error {
	idx := p.p.Index()
	p.t.mu.Lock()
	if buf := p.t.bufs[idx]; buf != nil {
		delete(p.t.bufs, idx) // failed the hash check: discard
		p.t.buffered -= int64(len(buf.data))
	}
	p.t.mu.Unlock()
	return p.t.pc.Set(p.key(), false)
}

// Completion trusts the database, but double-checks that the files covering
// the piece still exist at full size (they may have been deleted outside the
// app).
func (p *filePiece) Completion() storage.Completion {
	c, err := p.t.pc.Get(p.key())
	if err != nil {
		return storage.Completion{Err: err}
	}
	if !c.Ok || !c.Complete {
		return c
	}
	if p.t.blocked != nil {
		return storage.Completion{Ok: true, Complete: false}
	}
	ext := segments.Extent{Start: p.p.Offset(), Length: p.p.Length()}
	for i, e := range p.t.index.LocateIter(ext) {
		fi, err := os.Stat(p.t.paths[i])
		if err != nil || fi.Size() < e.Start+e.Length {
			return storage.Completion{Ok: true, Complete: false}
		}
	}
	return c
}

func covered(buf *pieceBuf, off int64, n int) bool {
	for i := off / blockSize; i*blockSize < off+int64(n); i++ {
		if int(i) >= len(buf.have) || !buf.have[i] {
			return false
		}
	}
	return true
}

func overlay(buf *pieceBuf, b []byte, off int64) {
	for i := off / blockSize; i*blockSize < off+int64(len(b)); i++ {
		if int(i) >= len(buf.have) || !buf.have[i] {
			continue
		}
		lo := max(i*blockSize, off)
		hi := min((i+1)*blockSize, off+int64(len(b)), int64(len(buf.data)))
		copy(b[lo-off:hi-off], buf.data[lo:hi])
	}
}
