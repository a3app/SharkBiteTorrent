// Package scan checks torrents for malware: a quick look at the file list
// as soon as it's known (before downloading), marking finished files as
// downloaded from the internet, and scanning them with the system's
// antivirus where one is available.
package scan

import (
	"path"
	"strings"
)

type File struct {
	Path string // as listed in the torrent, "/"-separated
	Size int64
}

// Warning is a reason to be careful with a torrent, from its file list alone.
type Warning struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

var executable = set(
	// Windows programs and scripts
	".exe", ".scr", ".com", ".pif", ".bat", ".cmd", ".vbs", ".vbe", ".js", ".jse",
	".wsf", ".wsh", ".ps1", ".msi", ".msp", ".hta", ".cpl", ".jar", ".reg", ".lnk",
	// macOS and Linux
	".app", ".pkg", ".dmg", ".command", ".sh", ".run", ".appimage", ".desktop",
)

var media = set(
	".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v", ".webm", ".mpg", ".mpeg", ".ts", ".flv",
	".mp3", ".flac", ".m4a", ".aac", ".wav", ".ogg", ".opus",
	".srt", ".sub", ".ass", ".nfo", ".jpg", ".jpeg", ".png", ".txt",
)

var video = set(".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v", ".webm", ".mpg", ".mpeg", ".ts", ".flv")

var docLike = set(".pdf", ".doc", ".docx", ".txt", ".jpg", ".jpeg", ".png")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func ext(p string) string { return strings.ToLower(path.Ext(p)) }

// Inspect flags the classic torrent malware tricks: a program disguised as
// a video ("movie.mp4.exe"), a program or script shipped inside what is
// otherwise a video/music torrent, and shortcut (.lnk) files. Software
// torrents that are mostly programs aren't flagged, since that's expected.
func Inspect(files []File) []Warning {
	var out []Warning
	var mediaBytes, total int64
	hasVideo := false
	for _, f := range files {
		total += f.Size
		if media[ext(f.Path)] {
			mediaBytes += f.Size
		}
		if video[ext(f.Path)] {
			hasVideo = true
		}
	}
	mostlyMedia := total > 0 && mediaBytes*10 >= total*8 // 80%+ of the data

	for _, f := range files {
		e := ext(f.Path)
		if !executable[e] {
			continue
		}
		base := strings.ToLower(path.Base(f.Path))
		inner := path.Ext(strings.TrimSuffix(base, e))
		switch {
		case inner != "" && (media[inner] || docLike[inner]):
			out = append(out, Warning{f.Path, "A program disguised as a " + strings.TrimPrefix(inner, ".") + " file (double extension)."})
		case e == ".lnk":
			out = append(out, Warning{f.Path, "A Windows shortcut. Shortcuts in downloads are a common way to run hidden commands."})
		case mostlyMedia || hasVideo:
			out = append(out, Warning{f.Path, "A program or script inside a video/music torrent. Media files never need one to play."})
		}
	}
	return out
}
