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
	// Top is the torrent's name when it's a folder of files (multi-file
	// torrents' paths don't include it): a program in a folder named like
	// a film is as suspicious as one named like a film.
	Top string
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

// releaseWord finds the words video release names carry. A program named
// like that is a fake "movie": "You Can See Everything 2026.1080p. HD
// h264- ETHE.exe" was a 1.2 GB Windows program that ClamAV scanned in full
// and passed. One strong word is enough (a resolution, a rip source, a
// release group, an episode number); weak ones (a codec, a format, 4K) need
// a second, since software is named after them too ("x265.exe", "4K Video
// Downloader", a "BluRay ripper", an "HEVC" codec pack).
func releaseWord(words []string) func(string) string {
	var res []*regexp.Regexp
	for _, w := range words {
		res = append(res, regexp.MustCompile(`(?i)(^|[^a-z0-9])(`+w+`)($|[^a-z0-9])`))
	}
	return func(s string) string {
		for _, re := range res {
			if m := re.FindStringSubmatch(s); m != nil {
				return m[2]
			}
		}
		return ""
	}
}

var (
	strongRelease = releaseWord([]string{`480p`, `576p`, `720p`, `1080[pi]`, `1440p`, `2160p`,
		`web-?dl`, `web-?rip`, `bdrip`, `brrip`, `bdremux`, `remux`, `hdrip`, `dvdrip`, `dvdscr`,
		`hdtv`, `hdcam`, `camrip`, `telesync`, `yts`, `yify`, `rarbg`, `s\d{1,2}e\d{1,3}`})
	weakRelease = []func(string) string{
		releaseWord([]string{`x26[45]`, `h\.?26[45]`, `hevc`, `xvid`, `divx`, `av1`}),
		releaseWord([]string{`blu-?ray`}),
		releaseWord([]string{`hdr10\+?`, `hdr`, `dolby ?vision`, `dv`}),
		releaseWord([]string{`4k`, `uhd`, `fullhd`}),
		releaseWord([]string{`10bit`}),
		releaseWord([]string{`ddp?5\.1`, `atmos`, `aac`, `dts`}),
	}
)

// videoRelease returns a release word in s, if s is named like a video
// release ("" if not).
func videoRelease(s string) string {
	if w := strongRelease(s); w != "" {
		return w
	}
	var found []string
	for _, kind := range weakRelease {
		if w := kind(s); w != "" {
			found = append(found, w)
		}
	}
	if len(found) >= 2 {
		return found[0] + ", " + found[1]
	}
	return ""
}

// diskImage files are in executable (opening one gets around Mark of the
// Web) but a full-disc film rip is one too, so they aren't judged by name.
var diskImage = set(".iso", ".img", ".vhd", ".vhdx")

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
		// Spaces before the real extension ("movie.mp4      .exe") hide it
		// in narrow columns.
		inner := path.Ext(strings.TrimRight(strings.TrimSuffix(base, e), ". "))
		switch {
		case inner != "" && (media[inner] || docLike[inner]):
			out = append(out, Warning{f.Path, "A program disguised as a " + strings.TrimPrefix(inner, ".") + " file (double extension)."})
		case e == ".lnk":
			out = append(out, Warning{f.Path, "A Windows shortcut. Shortcuts in downloads are a common way to run hidden commands."})
		case mostlyMedia || hasVideo:
			out = append(out, Warning{f.Path, "A program or script inside a video/music torrent. Media files never need one to play."})
		case diskImage[e]:
		default:
			// Its own name and the torrent's; not folders inside, where a
			// video tool keeps its encoders ("x265/x265.exe").
			if w := videoRelease(f.Top + "/" + path.Base(f.Path)); w != "" {
				out = append(out, Warning{f.Path, "A program named like a video release (\"" + w + "\"). Real videos are files like .mkv or .mp4: opening this would run a program, not play a film."})
			}
		}
	}
	return out
}
