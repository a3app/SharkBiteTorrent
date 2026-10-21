package socks

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProxy(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	}))
	defer site.Close()

	var dialed []string
	d := &net.Dialer{}
	s := &Server{Username: "u", Password: "secret", Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return d.DialContext(ctx, network, addr)
	}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	defer s.Close()

	get := func(user, pass string) (string, error) {
		proxy := &url.URL{Scheme: "socks5", Host: ln.Addr().String(), User: url.UserPassword(user, pass)}
		hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
		resp, err := hc.Get(site.URL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	if body, err := get("u", "secret"); err != nil || body != "hello" {
		t.Fatalf("through proxy: %q %v", body, err)
	}
	if s.Requests.Load() != 1 {
		t.Errorf("requests counted: %d", s.Requests.Load())
	}
	if len(dialed) != 1 || dialed[0] != site.Listener.Addr().String() {
		t.Errorf("dialed %v", dialed)
	}
	if _, err := get("u", "wrong"); err == nil {
		t.Error("wrong password was accepted")
	}
	if len(dialed) != 1 || s.Requests.Load() != 1 {
		t.Error("dialed out for (or counted) an unauthenticated client")
	}
}

// A client that keeps its connection open sends its next request without a
// new CONNECT; Sent must still show it, or the app's proxy check reports a
// search server that is using the proxy as not using it.
func TestSentCountsReusedConnections(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	}))
	defer site.Close()
	d := &net.Dialer{}
	s := &Server{Username: "u", Password: "secret", Dial: d.DialContext}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	defer s.Close()

	proxy := &url.URL{Scheme: "socks5", Host: ln.Addr().String(), User: url.UserPassword("u", "secret")}
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	get := func() {
		resp, err := hc.Get(site.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	get()
	reqs, sent := s.Requests.Load(), s.Sent.Load()
	if sent == 0 {
		t.Fatal("the first request's bytes weren't counted")
	}
	get()
	if s.Requests.Load() != reqs {
		t.Fatalf("expected the second request to reuse the connection (requests %d → %d)", reqs, s.Requests.Load())
	}
	if s.Sent.Load() <= sent {
		t.Error("a request over a reused connection wasn't counted in Sent")
	}
}
