package engine

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// ownership records which of a torrent's files this app created in one
// download folder, so the store only ever writes into and deletes its own
// files: never a file that was there before (the user's, or another
// torrent's with the same name).
//
// A file is recorded when the store creates it (O_EXCL), with its identity
// (device and inode, or Windows' volume serial and file index; see fileID)
// and size. It counts as ours only while the file at that path still is
// that file, so a record can't vouch for a different file that later
// appears at the same path; entries that no longer match are dropped.
//
// Records are owned/<info hash>-<folder hash> in the app's data folder: a
// header naming the folder, then "id<TAB>size<TAB>path" per file, appended
// as files are created. Torrents saved by older versions adopt their
// existing files once, from the saved torrent list (claim with adopt).
type ownership struct {
	path string // the record file
	dir  string // download folder

	mu      sync.Mutex
	owned   map[string]fileRec // by absolute path
	written bool               // the record file exists with its header
}

type fileRec struct {
	id   string
	size int64
}

const ownershipHeader = "sbt-owned 2\t"

var errNotOurs = errors.New("a file at this path already exists and wasn't created by Shark BiteTorrent")

func recordPath(ownedDir string, ih metainfo.Hash, dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return filepath.Join(ownedDir, ih.HexString()+"-"+hex.EncodeToString(sum[:8]))
}

// loadOwnership reads the torrent's record for dir, keeping only entries
// whose file is still the one recorded.
func loadOwnership(ownedDir string, ih metainfo.Hash, dir string) *ownership {
	o := &ownership{path: recordPath(ownedDir, ih, dir), dir: filepath.Clean(dir), owned: map[string]fileRec{}}
	b, err := os.ReadFile(o.path)
	if err != nil {
		return o
	}
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[:i+1] // a last line without its newline was cut off by a crash
	} else {
		return o
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	if !sc.Scan() || filepath.Clean(strings.TrimPrefix(sc.Text(), ownershipHeader)) != o.dir || !strings.HasPrefix(sc.Text(), ownershipHeader) {
		return o // not a record for this folder
	}
	o.written = true
	stale := false
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 3)
		if len(f) != 3 {
			stale = true
			continue
		}
		size, err := strconv.ParseInt(f[1], 10, 64)
		p := filepath.Join(o.dir, filepath.FromSlash(f[2]))
		if err != nil || !isSub(o.dir, p) || p == o.dir {
			stale = true
			continue
		}
		if id, sz, err := fileID(p); err != nil || id != f[0] || sz != size {
			stale = true // gone, or a different file now
			continue
		}
		o.owned[p] = fileRec{f[0], size}
	}
	if stale {
		o.mu.Lock()
		o.saveLocked()
		o.mu.Unlock()
	}
	return o
}

// has reports whether the file at p is one the store created.
func (o *ownership) has(p string) bool {
	o.mu.Lock()
	r, ok := o.owned[p]
	o.mu.Unlock()
	if !ok {
		return false
	}
	id, size, err := fileID(p)
	return err == nil && id == r.id && size == r.size
}

// add records a file the store has just created (and sized).
func (o *ownership) add(p string) error {
	id, size, err := fileID(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(o.dir, p)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.written {
		f, err := os.OpenFile(o.path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%s\t%d\t%s\n", id, size, filepath.ToSlash(rel))
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return werr
			}
			o.owned[p] = fileRec{id, size} // only once it's on disk
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// The record was deleted meanwhile: write it afresh.
	}
	o.owned[p] = fileRec{id, size}
	if err := o.saveLocked(); err != nil {
		delete(o.owned, p)
		return err
	}
	return nil
}

// adopt records existing files as ours: torrents from the saved list of an
// older version, which wrote no records.
func (o *ownership) adopt(paths []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range paths {
		if id, size, err := fileID(p); err == nil {
			o.owned[p] = fileRec{id, size}
		}
	}
	return o.saveLocked()
}

// saveLocked rewrites the whole record. Call with o.mu held.
func (o *ownership) saveLocked() error {
	var b strings.Builder
	b.WriteString(ownershipHeader + o.dir + "\n")
	paths := make([]string, 0, len(o.owned))
	for p := range o.owned {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if rel, err := filepath.Rel(o.dir, p); err == nil {
			r := o.owned[p]
			fmt.Fprintf(&b, "%s\t%d\t%s\n", r.id, r.size, filepath.ToSlash(rel))
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(o.path), filepath.Base(o.path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.WriteString(b.String())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), o.path)
	}
	if werr != nil {
		os.Remove(f.Name())
		return werr
	}
	o.written = true
	return nil
}

// list returns the recorded files that are still the ones created.
func (o *ownership) list() []string {
	o.mu.Lock()
	paths := make([]string, 0, len(o.owned))
	for p := range o.owned {
		paths = append(paths, p)
	}
	o.mu.Unlock()
	sort.Strings(paths)
	out := paths[:0]
	for _, p := range paths {
		if o.has(p) {
			out = append(out, p)
		}
	}
	return out
}

func (o *ownership) forget() { os.Remove(o.path) }

// claim prepares the torrent's ownership record and checks that none of its
// paths is taken by a file that isn't ours. adopt is for a torrent from the
// saved list with no record yet (saved by an older version): what's at its
// paths was downloaded by it.
func (s *fileStore) claim(ih metainfo.Hash, info *metainfo.Info, paths []string) error {
	if s.ownedDir == "" {
		return nil // tests and tools: no records
	}
	os.Remove(filepath.Join(s.ownedDir, ih.HexString())) // pre-release marker: not trusted
	o := loadOwnership(s.ownedDir, ih, s.dir)
	switch {
	case !o.written && s.adopt:
		if err := o.adopt(paths); err != nil {
			return err
		}
	case !o.written:
		// New here: progress recorded for this torrent in another folder
		// (or before it was removed) doesn't apply to these files.
		clearComplete(s.pc, ih, info.NumPieces())
	}
	s.mu.Lock()
	s.own = o
	s.mu.Unlock()
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil && !o.has(p) {
			rel, _ := filepath.Rel(s.dir, p)
			return fmt.Errorf("%q already exists in the download folder, and Shark BiteTorrent only writes into files it created. Move it away or choose another download folder, then add the torrent again", rel)
		}
	}
	return nil
}

// clearComplete forgets the torrent's finished pieces (only those: each
// write is a disk sync).
func clearComplete(pc storage.PieceCompletion, ih metainfo.Hash, n int) {
	for i := range n {
		key := metainfo.PieceKey{InfoHash: ih, Index: i}
		if c, err := pc.Get(key); err == nil && c.Ok && c.Complete {
			pc.Set(key, false)
		}
	}
}
