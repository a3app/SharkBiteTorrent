package search

import (
	"context"
	"net"
	"os"
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
	cfg := Config{Kind: Prowlarr, URL: srv.URL}
	src, tmp, err := Fetch(context.Background(), cfg, Result{Magnet: srv.URL + "/3/download?apikey=secret&file=A+%5B1080p%5D"}, nil)
	if err != nil || src != mag || tmp != "" {
		t.Fatalf("got %q %q %v", src, tmp, err)
	}
	_, _, err = Fetch(context.Background(), Config{URL: "http://127.0.0.1:1"}, Result{Download: "http://127.0.0.1:1/download?apikey=secret"}, nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks key or is nil: %v", err)
	}
}

// A redirect from the user's server to another site (an indexer's direct
// link) must go through the VPN, not the direct connection.
func TestFetchOtherHostsUseTunnel(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("d8:announce0:e")) // a (tiny) bencoded torrent
	}))
	defer site.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, site.URL+"/file.torrent", http.StatusFound)
	}))
	defer server.Close()
	cfg := Config{Kind: Prowlarr, URL: server.URL}
	r := Result{Download: server.URL + "/1/download?apikey=k"}

	// No tunnel: the server is reachable, but the redirect to the site is refused.
	if _, _, err := Fetch(context.Background(), cfg, r, nil); err == nil || !strings.Contains(err.Error(), "VPN not connected") {
		t.Fatalf("redirect went out directly: %v", err)
	}

	var tunneled []string
	tunnel := func(ctx context.Context, network, addr string) (net.Conn, error) {
		tunneled = append(tunneled, addr)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	src, tmp, err := Fetch(context.Background(), cfg, r, tunnel)
	if err != nil || tmp == "" || src != tmp {
		t.Fatalf("got %q %q %v", src, tmp, err)
	}
	os.Remove(tmp)
	if len(tunneled) != 1 || tunneled[0] != site.Listener.Addr().String() {
		t.Errorf("tunneled %v, want only the site", tunneled)
	}
}
