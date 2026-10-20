package scan

import "testing"

func TestInspect(t *testing.T) {
	cases := []struct {
		name  string
		files []File
		want  int
	}{
		{"plain movie", []File{{"Movie/movie.mkv", 2 << 30}, {"Movie/movie.srt", 50 << 10}}, 0},
		{"double extension", []File{{"movie.mp4.exe", 900 << 10}}, 1},
		{"program in a movie torrent", []File{{"Movie/movie.mkv", 2 << 30}, {"Movie/Codec Installer.exe", 2 << 20}}, 1},
		{"shortcut", []File{{"Album/Play me.lnk", 2 << 10}, {"Album/track.mp3", 5 << 20}}, 1},
		{"software is expected to have programs", []File{{"Tool/setup.exe", 80 << 20}, {"Tool/readme.txt", 2 << 10}}, 0},
		{"linux iso", []File{{"debian.iso", 700 << 20}}, 0},
	}
	for _, c := range cases {
		if got := Inspect(c.files); len(got) != c.want {
			t.Errorf("%s: got %d warnings %+v, want %d", c.name, len(got), got, c.want)
		}
	}
}

func TestInspectBypasses(t *testing.T) {
	for _, files := range [][]File{
		{{"Movie/movie.mp4.exe.", 1}, {"Movie/movie.mkv", 1 << 30}},   // trailing dot
		{{"Movie/movie.mp4.exe ", 1}, {"Movie/movie.mkv", 1 << 30}},   // trailing space
		{{"Invoice/Cute\u202egpj.exe", 1}},                            // right-to-left override
		{{"Movie/Watch online.url", 1}, {"Movie/movie.mkv", 1 << 30}}, // shortcut to a site
		{{"Movie/codec.iso", 1}, {"Movie/movie.mkv", 1 << 30}},        // disk image
	} {
		if w := Inspect(files); len(w) == 0 {
			t.Errorf("no warning for %v", files)
		}
	}
	// A Linux ISO torrent is fine.
	if w := Inspect([]File{{"archlinux.iso", 1 << 30}}); len(w) != 0 {
		t.Errorf("Linux ISO flagged: %v", w)
	}
	if got := StripBidiControls("Cute\u202egpj.exe"); got != "Cutegpj.exe" {
		t.Errorf("StripBidiControls: %q", got)
	}
}
