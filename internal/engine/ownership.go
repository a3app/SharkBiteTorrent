package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// Before a torrent writes anything in a download folder, claim checks that
// none of its paths is already taken: a file there is the user's own, or
// another torrent's, so the torrent is blocked instead of writing into it.
// Once a torrent passes, a marker (owned/<info hash>-<folder hash> in the
// app's data folder) records that its files there are its own, so restarts
// don't block it on its own files. Torrents saved by an older version have
// no marker yet; they're adopted once, from the saved list (Adopt).
//
// This is a check at the start, not per file: files that appear at a
// torrent's paths later (another torrent with the same name downloading at
// the same time, a file ticked later) aren't caught. Stricter per-file
// ownership is being worked on separately.

func markerPath(ownedDir string, ih metainfo.Hash, dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return filepath.Join(ownedDir, ih.HexString()+"-"+hex.EncodeToString(sum[:8]))
}

// claim checks that the torrent may write its files in s.dir.
func (s *fileStore) claim(ih metainfo.Hash, info *metainfo.Info, paths []string) error {
	if s.ownedDir == "" {
		return nil // tests and tools: no markers
	}
	os.Remove(filepath.Join(s.ownedDir, ih.HexString())) // pre-release marker: not trusted
	marker := markerPath(s.ownedDir, ih, s.dir)
	if _, err := os.Stat(marker); err == nil {
		return nil // passed before, in this folder
	}
	if !s.adopt {
		if err := noLinkedFolders(s.dir, paths); err != nil {
			return err
		}
		for _, p := range paths {
			if _, err := os.Lstat(p); err == nil {
				rel, _ := filepath.Rel(s.dir, p)
				return fmt.Errorf("%q already exists in the download folder, and Shark BiteTorrent only writes into files it created. Remove this torrent, move the file away (or choose another download folder), then add the torrent again", rel)
			}
		}
		// New here: progress recorded for this torrent in another folder
		// (or before it was removed) doesn't apply to these files.
		clearComplete(s.pc, ih, info.NumPieces())
	}
	if err := os.MkdirAll(s.ownedDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, nil, 0o600)
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

// noLinkedFolders refuses a torrent whose folders inside dir include a link
// (symlink, or junction on Windows) that's already there: the torrent would
// write into wherever it points, outside the download folder.
func noLinkedFolders(dir string, paths []string) error {
	seen := map[string]bool{}
	for _, p := range paths {
		for d := filepath.Dir(p); d != filepath.Clean(dir) && isSub(dir, d) && !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			if fi, err := os.Lstat(d); err == nil && !fi.IsDir() {
				rel, _ := filepath.Rel(dir, d)
				return fmt.Errorf("%q in the download folder is a link or a file, not a folder, and the torrent's files would go there. Remove this torrent, move it away (or choose another download folder), then add the torrent again", rel)
			}
		}
	}
	return nil
}
