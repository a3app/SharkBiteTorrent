// Package scan checks torrents for malware: a quick look at the file list
// as soon as it's known (before downloading), marking finished files as
// downloaded from the internet, and scanning them with the system's
// antivirus where one is available.
package scan

import (
	"path"
	"regexp"
	"strings"
	"unicode"
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
	".url", ".scf", ".chm", ".msix", ".appx", ".xll", ".library-ms", ".py",
	// Office documents with macros
	".docm", ".xlsm", ".pptm",
	// Disk images: opening one gets around Mark of the Web
	".iso", ".img", ".vhd", ".vhdx",
	// macOS and Linux
	".app", ".pkg", ".dmg", ".command", ".sh", ".run", ".appimage", ".desktop",
	".terminal", ".workflow", ".scpt", ".webloc", ".inetloc", ".fileloc", ".deb", ".rpm",
)

var media = set(
	".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v", ".webm", ".mpg", ".mpeg", ".ts", ".flv",
	".mp3", ".flac", ".m4a", ".aac", ".wav", ".ogg", ".opus",
	".srt", ".sub", ".ass", ".nfo", ".jpg", ".jpeg", ".png", ".txt",
)

var video = set(".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v", ".webm", ".mpg", ".mpeg", ".ts", ".flv")

// videoRelease matches the words video release names carry (resolution,
// codec, source, episode number). A program named like that is a fake
// "movie": "You Can See Everything 2026.1080p. HD h264- ETHE.exe" was a
// 1.2 GB Windows program that ClamAV scanned in full and passed. Ambiguous
// words ("4K", "HD") are left out, so software isn't flagged for them.
var videoRelease = regexp.MustCompile(`(?i)(^|[^a-z0-9])(480p|576p|720p|1080[pi]|1440p|2160p|x26[45]|h\.?26[45]|hevc|xvid|divx|web-?dl|web-?rip|blu-?ray|bdrip|brrip|hdrip|dvdrip|dvdscr|hdtv|hdcam|camrip|telesync|s\d{1,2}e\d{1,3})($|[^a-z0-9])`)

var docLike = set(".pdf", ".doc", ".docx", ".txt", ".jpg", ".jpeg", ".png")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// ext is the extension as the OS will see it: Windows drops trailing dots
// and spaces, so "movie.mp4.exe." is a .exe there.
func ext(p string) string { return strings.ToLower(path.Ext(strings.TrimRight(p, ". "))) }

// HasBidiControl reports characters that change the direction text is shown
// in (U+202E and friends), which can make "gpj.exe" show as "exe.jpg".
func HasBidiControl(s string) bool { return strings.IndexFunc(s, isBidiControl) >= 0 }

// StripBidiControls removes them, for showing names.
func StripBidiControls(s string) string {
	if !HasBidiControl(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isBidiControl(r) {
			return -1
		}
		return r
	}, s)
}

func isBidiControl(r rune) bool { return unicode.Is(unicode.Bidi_Control, r) }

// Inspect flags the classic torrent malware tricks: a program disguised as
// a video ("movie.mp4.exe", or one named like a video release: "Film
// 1080p x264.exe"), a program or script shipped inside what is otherwise a
// video/music torrent, and shortcut (.lnk) files. Software
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
		if HasBidiControl(f.Path) {
			out = append(out, Warning{StripBidiControls(f.Path), "The name contains invisible characters that reverse how text is shown, a trick to disguise a program's real extension."})
			continue
		}
		e := ext(f.Path)
		if !executable[e] {
			continue
		}
		base := strings.ToLower(strings.TrimRight(path.Base(f.Path), ". "))
		inner := path.Ext(strings.TrimSuffix(base, e))
		switch {
		case inner != "" && (media[inner] || docLike[inner]):
			out = append(out, Warning{f.Path, "A program disguised as a " + strings.TrimPrefix(inner, ".") + " file (double extension)."})
		case e == ".lnk":
			out = append(out, Warning{f.Path, "A Windows shortcut. Shortcuts in downloads are a common way to run hidden commands."})
		case mostlyMedia || hasVideo:
			out = append(out, Warning{f.Path, "A program or script inside a video/music torrent. Media files never need one to play."})
		default:
			// The torrent's folder is part of the path, so a program in a
			// folder named like a film counts too.
			if m := videoRelease.FindStringSubmatch(f.Path); m != nil {
				out = append(out, Warning{f.Path, "A program named like a video release (\"" + m[2] + "\"). Real videos are files like .mkv or .mp4: opening this would run a program, not play a film."})
			}
		}
	}
	return out
}
