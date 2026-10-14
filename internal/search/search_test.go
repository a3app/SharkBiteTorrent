package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDedupe(t *testing.T) {
	rs := dedupe([]Result{
		{Title: "A", Size: 1, Indexer: "x"},
		{Title: "A", Size: 1, Indexer: "x"}, // exact repeat
		{Title: "A", Size: 1, Indexer: "y"}, // same release, other site: kept
		{Title: "A", Size: 2, Indexer: "x"}, // different size: kept
	})
	if len(rs) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(rs), rs)
	}
}

func TestFetchProxyMagnet(t *testing.T) {
	const mag = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, mag, http.StatusMovedPermanently)
	}))
	defer srv.Close()
	src, tmp, err := Fetch(context.Background(), Result{Magnet: srv.URL + "/3/download?apikey=secret&file=A+%5B1080p%5D"})
	if err != nil || src != mag || tmp != "" {
		t.Fatalf("got %q %q %v", src, tmp, err)
	}
	_, _, err = Fetch(context.Background(), Result{Download: "http://127.0.0.1:1/download?apikey=secret"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks key or is nil: %v", err)
	}
}
