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
