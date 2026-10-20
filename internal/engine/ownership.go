package engine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// ownership records which of a torrent's files this app created, for one
// download folder, so the store only ever writes into and deletes its own
// files: never a file that was there before (the user's, or another
// torrent's with the same name). A file is recorded when the store creates
// it with O_EXCL, so a file that appeared at the same path some other way
// is never taken for ours.
//
// The record is owned/<info hash> in the app's data folder: a "dir" line
// naming the download folder, then one path per created file, relative to
// it, appended as files are created. Older versions wrote an empty marker
// file; those torrents' existing files are adopted once (see claim).
type ownership struct {
	path string // the record file
	dir  string // download folder

	mu      sync.Mutex
	owned   map[string]bool // absolute paths
	written bool            // the record file has this dir's header
}

var errNotOurs = errors.New("a file at this path already exists and wasn't created by Shark BiteTorrent")

// loadOwnership reads the torrent's record. legacy is true for an older
// version's empty marker, absent for no record at all; a record for another
// folder counts as neither (nothing is ours here).
func loadOwnership(ownedDir string, ih metainfo.Hash, dir string) (o *ownership, legacy, absent bool) {
	o = &ownership{path: filepath.Join(ownedDir, ih.HexString()), dir: dir, owned: map[string]bool{}}
	b, err := os.ReadFile(o.path)
	if err != nil {
		return o, false, true
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return o, true, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if d, ok := strings.CutPrefix(line, "dir\t"); !ok || filepath.Clean(d) != filepath.Clean(dir) {
				return o, false, false // a record for another folder
			}
			o.written = true
			continue
		}
		if line != "" {
			o.owned[filepath.Join(dir, filepath.FromSlash(line))] = true
		}
	}
	return o, false, false
}

func (o *ownership) has(p string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owned[p]
}

// add records a file the store has just created.
func (o *ownership) add(p string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.owned[p] = true
	if !o.written {
		return o.saveLocked()
	}
	rel, err := filepath.Rel(o.dir, p)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(o.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintln(f, filepath.ToSlash(rel))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// adopt records existing files as ours (torrents from older versions).
func (o *ownership) adopt(paths []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			o.owned[p] = true
		}
	}
	return o.saveLocked()
}

// saveLocked rewrites the whole record for this folder. Call with o.mu held.
func (o *ownership) saveLocked() error {
	var b strings.Builder
	fmt.Fprintf(&b, "dir\t%s\n", o.dir)
	for _, p := range o.listLocked() {
		if rel, err := filepath.Rel(o.dir, p); err == nil {
			fmt.Fprintln(&b, filepath.ToSlash(rel))
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		return err
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, o.path); err != nil {
		return err
	}
	o.written = true
	return nil
}

func (o *ownership) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.listLocked()
}

func (o *ownership) listLocked() []string {
	out := make([]string, 0, len(o.owned))
	for p := range o.owned {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (o *ownership) forget() { os.Remove(o.path) }

// claim prepares the torrent's ownership record and checks that none of its
// paths is taken by a file that isn't ours.
func (s *fileStore) claim(ih metainfo.Hash, info *metainfo.Info, paths []string) error {
	if s.ownedDir == "" {
		return nil // tests and tools: no records
	}
	o, legacy, absent := loadOwnership(s.ownedDir, ih, s.dir)
	switch {
	case legacy || absent && anyComplete(s.pc, ih, info.NumPieces()):
		// From before per-file records: what's there was this torrent's.
		if err := o.adopt(paths); err != nil {
			return err
		}
	case !o.written:
		// New here (or moved to another folder): progress recorded for this
		// torrent elsewhere doesn't apply to these files.
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

func anyComplete(pc storage.PieceCompletion, ih metainfo.Hash, n int) bool {
	for i := range n {
		if c, err := pc.Get(metainfo.PieceKey{InfoHash: ih, Index: i}); err == nil && c.Ok && c.Complete {
			return true
		}
	}
	return false
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
