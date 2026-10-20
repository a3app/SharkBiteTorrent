// Package sfv checks files against .sfv lists (CRC32 per file), which some
// torrents include. It's an extra, independent check on top of BitTorrent's
// own piece hashes: it confirms the files match what the release's creator
// listed, file by file.
package sfv

import (
	"bufio"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

type Entry struct {
	Name string // as written in the .sfv, with forward slashes
	CRC  uint32
}

// Parse reads an .sfv file: "name CRC32" per line, ";" starts a comment.
// Names may contain spaces; the CRC is the last field.
func Parse(r io.Reader) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		i := strings.LastIndexAny(line, " \t")
		if i < 0 {
			continue
		}
		crc, err := strconv.ParseUint(line[i+1:], 16, 32)
		if err != nil || len(line[i+1:]) != 8 {
			continue
		}
		name := strings.ReplaceAll(strings.TrimSpace(line[:i]), `\`, "/")
		if name != "" {
			out = append(out, Entry{Name: name, CRC: uint32(crc)})
		}
	}
	return out, sc.Err()
}

// Result of checking every .sfv in a torrent.
type Result struct {
	Checked int      // files whose CRC matched
	Bad     []string // files whose CRC didn't match
	Missing []string // listed files that aren't on disk (not downloaded, or not wanted)
}

// Find returns the torrent paths of .sfv files among files.
func Find(files map[string]string) []string {
	var out []string
	for rel := range files {
		if strings.EqualFold(path.Ext(rel), ".sfv") {
			out = append(out, rel)
		}
	}
	return out
}

// Limits, so a malicious .sfv (the same big file listed thousands of times,
// or millions of made-up names) can't keep the app hashing or bloat the saved
// torrent list: each file is hashed once, entries past maxEntries are
// ignored, and at most maxListed names are kept per list.
const (
	maxEntries = 10000
	maxListed  = 50
)

// Check verifies every .sfv among files. files maps each file's path inside
// the torrent (forward slashes) to where it is on disk. Names in an .sfv are
// relative to its own folder.
func Check(ctx context.Context, files map[string]string) (res Result, err error) {
	var moreBad, moreMissing int
	defer func() {
		if moreBad > 0 {
			res.Bad = append(res.Bad, fmt.Sprintf("…and %d more", moreBad))
		}
		if moreMissing > 0 {
			res.Missing = append(res.Missing, fmt.Sprintf("…and %d more", moreMissing))
		}
	}()
	seen := map[string]bool{}   // entries already checked (by path in the torrent)
	crcs := map[string]uint32{} // CRCs already computed (by file on disk)
	entriesLeft := maxEntries
	lower := map[string]string{} // case-insensitive fallback, as on Windows
	for rel, disk := range files {
		lower[strings.ToLower(rel)] = disk
	}
	for _, sfvRel := range Find(files) {
		f, err := os.Open(files[sfvRel])
		if err != nil {
			return res, err
		}
		entries, err := Parse(f)
		f.Close()
		if err != nil {
			return res, fmt.Errorf("%s: %w", sfvRel, err)
		}
		dir := path.Dir(sfvRel)
		for _, e := range entries {
			rel := path.Clean(path.Join(dir, e.Name))
			if seen[rel] {
				continue
			}
			seen[rel] = true
			if entriesLeft--; entriesLeft < 0 {
				return res, nil
			}
			disk, ok := files[rel]
			if !ok {
				disk, ok = lower[strings.ToLower(rel)]
			}
			if !ok {
				if len(res.Missing) < maxListed {
					res.Missing = append(res.Missing, rel)
				} else {
					moreMissing++
				}
				continue
			}
			got, done := crcs[disk]
			if !done {
				var err error
				if got, err = fileCRC(ctx, disk); err != nil {
					return res, err
				}
				crcs[disk] = got
			}
			if got == e.CRC {
				res.Checked++
			} else if len(res.Bad) < maxListed {
				res.Bad = append(res.Bad, rel)
			} else {
				moreBad++
			}
		}
	}
	return res, nil
}

func fileCRC(ctx context.Context, name string) (uint32, error) {
	f, err := os.Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := crc32.NewIEEE()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			return h.Sum32(), nil
		}
		if err != nil {
			return 0, err
		}
	}
}
