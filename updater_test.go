package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.1", "0.2.0", true},
		{"0.2.10", "0.2.9", true},
		{"0.3", "0.2.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.2.1", false},
		{"0.2", "0.2.0", false},
		{"0.3.0", "0.3.0-rc1", true},
		{"0.3.0-rc1", "0.3.0", false},
		{"0.3.0-rc2", "0.3.0-rc1", true},
		{"0.3.0-rc1", "0.2.18", true},
		{"0.2.18", "0.3.0-rc1", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestFetchChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111  SharkBiteTorrent.exe\n" +
			"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222  SharkBiteTorrent-amd64-installer.exe\n"))
	}))
	defer srv.Close()
	got, err := fetchChecksum(context.Background(), srv.Client(), srv.URL, installerAsset)
	if err != nil || got != "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := fetchChecksum(context.Background(), srv.Client(), srv.URL, "missing.exe"); err == nil {
		t.Fatal("expected error for unlisted file")
	}
}
