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

// Check verifies every .sfv among files. files maps each file's path inside
// the torrent (forward slashes) to where it is on disk. Names in an .sfv are
// relative to its own folder.
func Check(ctx context.Context, files map[string]string) (Result, error) {
	var res Result
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
			disk, ok := files[rel]
			if !ok {
				disk, ok = lower[strings.ToLower(rel)]
			}
			if !ok {
				res.Missing = append(res.Missing, rel)
				continue
			}
			got, err := fileCRC(ctx, disk)
			if err != nil {
				return res, err
			}
			if got == e.CRC {
				res.Checked++
			} else {
				res.Bad = append(res.Bad, rel)
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
