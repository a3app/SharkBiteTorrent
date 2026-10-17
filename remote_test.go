package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sharkbite/internal/search"
	"strconv"
	"strings"
	"testing"
	"time"
)

func remoteRequest(h http.Handler, method, path, from, host, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = from
	r.Host = host
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if method == "POST" && strings.HasPrefix(body, "{") {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func newTestRemote(t *testing.T) (*App, *remoteServer, http.Handler) {
	a := &App{configDir: t.TempDir()}
	rs := &remoteServer{port: 8732, addrs: []string{"192.168.1.5"}, keyFails: map[string][]time.Time{}, codeFails: map[string][]time.Time{}}
	return a, rs, a.remoteHandler(rs)
}

func pairPhone(t *testing.T, rs *remoteServer, h http.Handler, from string) string {
	t.Helper()
	rs.pairCode, rs.pairUntil, rs.pairFailures = newPairCode(), time.Now().Add(time.Minute), 0
	w := remoteRequest(h, "POST", "/api/pair", from, "192.168.1.5:8732", "", `{"code":"`+rs.pairCode+`","name":"Test phone"}`)
	var got struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || len(got.Token) < 40 {
		t.Fatalf("pair: %d %q", w.Code, w.Body.String())
	}
	return got.Token
}

func TestRemoteSecurity(t *testing.T) {
	a, rs, h := newTestRemote(t)
	lan, host := "192.168.1.20:5000", "192.168.1.5:8732"

	// Only private addresses.
	if w := remoteRequest(h, "GET", "/", "203.0.113.9:5000", host, "", ""); w.Code != http.StatusForbidden {
		t.Errorf("public address allowed: %d", w.Code)
	}
	// No domain names (DNS rebinding).
	if w := remoteRequest(h, "GET", "/", lan, "evil.example:8732", "", ""); w.Code != http.StatusMisdirectedRequest {
		t.Errorf("domain Host allowed: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/", lan, host, "", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "app.js") {
		t.Errorf("page: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/state", lan, host, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("state without key: %d", w.Code)
	}

	// Pairing must be JSON (a web page can't send that cross-site).
	rs.pairCode, rs.pairUntil = "K7QM4XRP", time.Now().Add(time.Minute)
	r := httptest.NewRequest("POST", "/api/pair", strings.NewReader(`{"code":"K7QM4XRP"}`))
	r.RemoteAddr, r.Host = lan, host
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain pairing accepted: %d", w.Code)
	}
	// Wrong code refused; the right one (typed with a dash, lower case) works once.
	if w := remoteRequest(h, "POST", "/api/pair", "192.168.1.30:5000", host, "", `{"code":"wrong","name":"x"}`); w.Code != http.StatusForbidden {
		t.Errorf("wrong code: %d", w.Code)
	}
	w = remoteRequest(h, "POST", "/api/pair", lan, host, "", `{"code":"k7qm-4xrp","name":"Test‮phone"}`)
	var got struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || len(got.Token) < 40 {
		t.Fatalf("pair: %d %q", w.Code, w.Body.String())
	}
	if w := remoteRequest(h, "POST", "/api/pair", lan, host, "", `{"code":"K7QM4XRP","name":"again"}`); w.Code != http.StatusForbidden {
		t.Errorf("pairing code reused: %d", w.Code)
	}
	// Stored: hash only, the address it paired from, a cleaned name.
	if d := a.store.Devices; len(d) != 1 || d[0].TokenHash == got.Token || d[0].IP != "192.168.1.20" || d[0].Name != "Testphone" {
		t.Errorf("devices: %+v", d)
	}

	// The key works and the phone only gets what it shows.
	w = remoteRequest(h, "GET", "/api/state", lan, host, got.Token, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "exitIP") || strings.Contains(w.Body.String(), `"dir"`) {
		t.Errorf("state: %d %s", w.Code, w.Body.String())
	}
	if w := remoteRequest(h, "POST", "/api/torrents/../remove", lan, host, got.Token, ""); w.Code == http.StatusOK {
		t.Errorf("bad hash accepted")
	}

	// Removing the device revokes the key.
	a.RemoveRemoteDevice(a.store.Devices[0].ID)
	if w := remoteRequest(h, "GET", "/api/state", lan, host, got.Token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked key accepted: %d", w.Code)
	}
}

func TestRemoteRateLimits(t *testing.T) {
	_, rs, h := newTestRemote(t)
	host := "192.168.1.5:8732"

	// A removed phone keeps polling with its old key and gets locked out of
	// the API, but can still pair again (separate counters).
	stale := "192.168.1.40:5000"
	for i := 0; i < 12; i++ {
		remoteRequest(h, "GET", "/api/state", stale, host, "old-key", "")
	}
	if w := remoteRequest(h, "GET", "/api/state", stale, host, "old-key", ""); w.Code != http.StatusTooManyRequests {
		t.Errorf("wrong keys not limited: %d", w.Code)
	}
	token := pairPhone(t, rs, h, stale)
	// …and a valid key is never rate-limited.
	if w := remoteRequest(h, "GET", "/api/state", stale, host, token, ""); w.Code != http.StatusOK {
		t.Errorf("valid key blocked by the rate limit: %d", w.Code)
	}

	// 20 wrong codes in total cancel the code, whatever the addresses.
	rs.pairCode, rs.pairUntil, rs.pairFailures = "ABCDEFGH", time.Now().Add(time.Minute), 0
	for i := 0; i < 20; i++ {
		remoteRequest(h, "POST", "/api/pair", "10.0.0."+strconv.Itoa(i+1)+":5000", host, "", `{"code":"WRONGGGG"}`)
	}
	if w := remoteRequest(h, "POST", "/api/pair", "10.0.1.1:5000", host, "", `{"code":"ABCDEFGH"}`); w.Code != http.StatusForbidden {
		t.Errorf("code still valid after 20 wrong guesses: %d", w.Code)
	}
	// IPv6 addresses in one /64 share a limit.
	if limitKey("fe80::1") != limitKey("fe80::abcd:1234") {
		t.Error("IPv6 /64 not grouped")
	}
	if c := newPairCode(); len(c) != 8 || normalizePairCode(c) != c {
		t.Errorf("pair code %q", c)
	}
}

func TestRemoteStoreFormats(t *testing.T) {
	a := &App{configDir: t.TempDir()}
	a.loadRemoteStore()
	if a.storeErr != nil || a.store.Home != nil {
		t.Fatalf("empty: %+v %v", a.store, a.storeErr)
	}
	a.store.Home = &lanNet{LocalIP: "192.168.1.5", Gateway: "192.168.1.1", GatewayMAC: "aa:bb:cc:dd:ee:ff"}
	a.store.Devices = []remoteDevice{{ID: "x", Name: "p"}}
	if err := a.saveRemoteStore(); err != nil {
		t.Fatal(err)
	}
	a.loadRemoteStore()
	if a.store.Home == nil || a.store.Home.GatewayMAC != "aa:bb:cc:dd:ee:ff" || len(a.store.Devices) != 1 {
		t.Errorf("round trip: %+v", a.store)
	}
	// Same router, new address for this computer: still home.
	if !(lanNet{LocalIP: "192.168.1.9", Gateway: "192.168.1.1", GatewayMAC: normalizeMAC("AA-BB-CC-DD-EE-FF")}).same(*a.store.Home) {
		t.Error("same router not recognized")
	}
	if (lanNet{Gateway: "192.168.1.1", GatewayMAC: "11:22:33:44:55:66"}).same(*a.store.Home) {
		t.Error("different router counted as home")
	}
}

// Search results reach the phone without their links (which carry the
// Prowlarr/Jackett API key), and downloading takes only an ID.
func TestRemoteSearchResultsHideLinks(t *testing.T) {
	_, rs, h := newTestRemote(t)
	token := pairPhone(t, rs, h, "192.168.1.20:5000")
	out := rs.keepResults([]search.Result{{
		Title: "Debian", Size: 1, Seeders: 5,
		Download: "http://localhost:9696/1/download?apikey=SECRET",
		Magnet:   "http://localhost:9696/1/download?apikey=SECRET&link=x",
	}})
	b, _ := json.Marshal(out)
	if len(out) != 1 || strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "9696") {
		t.Fatalf("phone sees links: %s", b)
	}
	if r, ok := rs.result(out[0].ID); !ok || r.Download == "" {
		t.Errorf("result not kept on the computer")
	}
	// A made-up ID (or a whole result with a URL of the phone's choosing) is refused.
	w := remoteRequest(h, "POST", "/api/search/add", "192.168.1.20:5000", "192.168.1.5:8732", token,
		`{"id":"nope","download":"http://evil.example/x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("unknown id: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteTailscaleClients(t *testing.T) {
	// Tailscale knows 100.90.1.2 (same account) and 100.90.9.9 (someone
	// else's device on the tailnet); anything else it doesn't know.
	rs := &remoteServer{tsAddr: "100.101.102.103", whois: func(ip string) tailscalePeer {
		return tailscalePeer{OK: ip == "100.90.1.2", Name: "pixel"}
	}}
	req := func(local, remote string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(local), Port: 8732}))
	}
	cases := []struct {
		local, remote string
		want          bool
	}{
		{"100.101.102.103", "100.90.1.2:5000", true},    // my Tailscale phone on the Tailscale address
		{"100.101.102.103", "100.90.9.9:5000", false},   // another account's device on the tailnet
		{"100.101.102.103", "100.70.0.5:5000", false},   // a 100.64/10 address Tailscale doesn't know (ISP, hotel)
		{"100.101.102.103", "192.168.4.39:5000", false}, // home device on the Tailscale address
		{"100.101.102.103", "203.0.113.5:5000", false},  // internet
		{"192.168.4.111", "192.168.4.39:5000", true},    // home phone at home
		{"192.168.4.111", "100.90.1.2:5000", false},     // 100.64/10 on the home address (e.g. ISP CGNAT)
	}
	for _, c := range cases {
		if got := rs.allowedClient(req(c.local, c.remote)); got != c.want {
			t.Errorf("local %s from %s: got %v", c.local, c.remote, got)
		}
	}
	// Without Tailscale access, a Tailscale address is just not private.
	if (&remoteServer{}).allowedClient(req("100.101.102.103", "100.90.1.2:5000")) {
		t.Error("Tailscale client allowed with Tailscale access off")
	}
}

// Pairing over Tailscale is tagged as such and never sets the home network.
func TestRemoteTailscalePairingKeepsHomeUnset(t *testing.T) {
	a := &App{configDir: t.TempDir()}
	rs := &remoteServer{port: 8732, addrs: []string{"100.101.102.103"}, tsAddr: "100.101.102.103",
		whois:    func(string) tailscalePeer { return tailscalePeer{OK: true, Name: "pixel-8"} },
		keyFails: map[string][]time.Time{}, codeFails: map[string][]time.Time{}}
	h := a.remoteHandler(rs)
	rs.pairCode, rs.pairUntil = "K7QM4XRP", time.Now().Add(time.Minute)
	r := httptest.NewRequest("POST", "/api/pair", strings.NewReader(`{"code":"K7QM4XRP","name":"Android phone"}`))
	r.RemoteAddr, r.Host = "100.90.1.2:5000", "100.101.102.103:8732"
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("100.101.102.103"), Port: 8732}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("pair over Tailscale: %d %s", w.Code, w.Body.String())
	}
	if a.store.Home != nil {
		t.Errorf("Tailscale pairing set the home network: %+v", a.store.Home)
	}
	if d := a.store.Devices; len(d) != 1 || !d[0].ViaTailscale || d[0].TSName != "pixel-8" {
		t.Errorf("device not tagged as Tailscale: %+v", d)
	}
}
