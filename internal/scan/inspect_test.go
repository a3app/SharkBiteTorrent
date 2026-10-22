package scan

import "testing"

func TestInspect(t *testing.T) {
	cases := []struct {
		name  string
		files []File
		want  int
	}{
		{"plain movie", []File{{"Movie/movie.mkv", 2 << 30, ""}, {"Movie/movie.srt", 50 << 10, ""}}, 0},
		{"double extension", []File{{"movie.mp4.exe", 900 << 10, ""}}, 1},
		{"program in a movie torrent", []File{{"Movie/movie.mkv", 2 << 30, ""}, {"Movie/Codec Installer.exe", 2 << 20, ""}}, 1},
		{"shortcut", []File{{"Album/Play me.lnk", 2 << 10, ""}, {"Album/track.mp3", 5 << 20, ""}}, 1},
		{"software is expected to have programs", []File{{"Tool/setup.exe", 80 << 20, ""}, {"Tool/readme.txt", 2 << 10, ""}}, 0},
		{"linux iso", []File{{"debian.iso", 700 << 20, ""}}, 0},
	}
	for _, c := range cases {
		if got := Inspect(c.files); len(got) != c.want {
			t.Errorf("%s: got %d warnings %+v, want %d", c.name, len(got), got, c.want)
		}
	}
}

func TestInspectBypasses(t *testing.T) {
	for _, files := range [][]File{
		{{"Movie/movie.mp4.exe.", 1, ""}, {"Movie/movie.mkv", 1 << 30, ""}},   // trailing dot
		{{"Movie/movie.mp4.exe ", 1, ""}, {"Movie/movie.mkv", 1 << 30, ""}},   // trailing space
		{{"Invoice/Cute\u202egpj.exe", 1, ""}},                                // right-to-left override
		{{"Movie/Watch online.url", 1, ""}, {"Movie/movie.mkv", 1 << 30, ""}}, // shortcut to a site
		{{"Movie/codec.iso", 1, ""}, {"Movie/movie.mkv", 1 << 30, ""}},        // disk image
	} {
		if w := Inspect(files); len(w) == 0 {
			t.Errorf("no warning for %v", files)
		}
	}
	// A Linux ISO torrent is fine.
	if w := Inspect([]File{{"archlinux.iso", 1 << 30, ""}}); len(w) != 0 {
		t.Errorf("Linux ISO flagged: %v", w)
	}
	if got := StripBidiControls("Cute\u202egpj.exe"); got != "Cutegpj.exe" {
		t.Errorf("StripBidiControls: %q", got)
	}
}

// A program named like a video release (or in a folder named like one) is a
// fake film; software with ordinary names isn't flagged.
func TestInspectVideoReleaseNames(t *testing.T) {
	for _, files := range [][]File{
		{{"You Can See Everything 2026.1080p. HD h264- ETHE.exe", 1283601623, ""}}, // the real one
		{{"Movie.2026.2160p.WEB-DL.DDP5.1/Movie.2026.2160p.WEB-DL.DDP5.1.exe", 900 << 20, ""}},
		{{Path: "setup.exe", Size: 40 << 20, Top: "Show.S01E02.720p.HDTV"}}, // multi-file: the name isn't in the path
		{{Path: "Film.scr", Size: 1 << 20, Top: "Film (2026) BluRay x265"}},
		{{"Film.2026.HDCAM/Film.2026.HDCAM.msi", 1 << 20, ""}},
		{{"Movie.2026.REMUX.HDR.exe", 1, ""}},
		{{"Movie (2026) [YTS] FullHD.exe", 1, ""}},
		{{"Movie.2026.UHD.4K.HDR10.DV.exe", 1, ""}},
		{{"movie.mp4          .exe", 1, ""}},          // padding before the real extension
		{{"movie.mp4\u3000\u3000.exe", 1, ""}},        // ideographic spaces
		{{"movie.mp4\u2800\u2800.exe", 1, ""}},        // Braille blanks
		{{"movie.mp4\u00a0\u200b.exe", 1, ""}},        // no-break space, zero-width space
		{{"Film.2026.1080p.WEB-DL.iso", 2 << 20, ""}}, // far too small to be a film
	} {
		if w := Inspect(files); len(w) != 1 {
			t.Errorf("%v: %d warnings %+v, want 1", files, len(w), w)
		}
	}
	for _, files := range [][]File{
		{{"VLC media player 3.0.21 x64/vlc-3.0.21-win64.exe", 40 << 20, ""}},
		{{"4K Video Downloader 4.33/setup.exe", 120 << 20, ""}},
		{{"HD Tune Pro 6.10/hdtunepro.exe", 5 << 20, ""}},
		{{"Some Game v1.0.1080/setup.exe", 8 << 30, ""}}, // a version number, not a resolution
		{{"Movie.2016.1080p.BluRay.iso", 40 << 30, ""}},  // a full-disc rip
		{{Path: "BDMV/disc.img", Size: 40 << 30, Top: "Movie (2016) BluRay"}},
		{{Path: "Apps/Encoders/x265/x265.exe", Size: 1 << 20, Top: "StaxRip 2.40"}}, // a tool's inner folder
		{{Path: "setup.exe", Size: 30 << 20, Top: "MakeMKV 1.17 BluRay ripper"}},
		{{Path: "K-Lite_Codec_Pack_Full.exe", Size: 60 << 20, Top: "K-Lite Codec Pack 18 with HEVC"}},
		{{"DivX Plus Player/DivXInstaller.exe", 20 << 20, ""}},
	} {
		if w := Inspect(files); len(w) != 0 {
			t.Errorf("%v flagged: %+v", files, w)
		}
	}
}
