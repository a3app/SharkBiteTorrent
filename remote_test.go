package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRemoteSecurity(t *testing.T) {
	a := &App{configDir: t.TempDir()}
	rs := &remoteServer{port: 8732, failures: map[string][]time.Time{}}
	h := a.remoteHandler(rs)
	lan, host := "192.168.1.20:5000", "192.168.1.5:8732"

	// Only the home network.
	if w := remoteRequest(h, "GET", "/", "203.0.113.9:5000", host, "", ""); w.Code != http.StatusForbidden {
		t.Errorf("public address allowed: %d", w.Code)
	}
	// No domain names (DNS rebinding).
	if w := remoteRequest(h, "GET", "/", lan, "evil.example:8732", "", ""); w.Code != http.StatusMisdirectedRequest {
		t.Errorf("domain Host allowed: %d", w.Code)
	}
	// The page itself loads.
	if w := remoteRequest(h, "GET", "/", lan, host, "", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "app.js") {
		t.Errorf("page: %d", w.Code)
	}
	// API needs a key.
	if w := remoteRequest(h, "GET", "/api/state", lan, host, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("state without key: %d", w.Code)
	}

	// Pairing: wrong code refused; right code works once.
	rs.pairCode, rs.pairUntil = "K7QM4XRP", time.Now().Add(time.Minute)
	if w := remoteRequest(h, "POST", "/api/pair", "192.168.1.30:5000", host, "", `{"code":"wrong","name":"x"}`); w.Code != http.StatusForbidden {
		t.Errorf("wrong code: %d", w.Code)
	}
	// Typed the way people type it: lower case, with a dash.
	w := remoteRequest(h, "POST", "/api/pair", lan, host, "", `{"code":"k7qm-4xrp","name":"Test phone"}`)
	var got struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || len(got.Token) < 40 {
		t.Fatalf("pair: %d %q", w.Code, w.Body.String())
	}
	if w := remoteRequest(h, "POST", "/api/pair", lan, host, "", `{"code":"K7QM4XRP","name":"again"}`); w.Code != http.StatusForbidden {
		t.Errorf("pairing code reused: %d", w.Code)
	}
	// The key works; a wrong key doesn't.
	if w := remoteRequest(h, "GET", "/api/state", lan, host, got.Token, ""); w.Code != http.StatusOK {
		t.Errorf("state with key: %d %s", w.Code, w.Body.String())
	}
	if w := remoteRequest(h, "GET", "/api/state", lan, host, got.Token+"x", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong key accepted: %d", w.Code)
	}
	// Only the hash is stored.
	if ds := a.loadRemoteDevices(); len(ds) != 1 || ds[0].TokenHash == got.Token || ds[0].Name != "Test phone" {
		t.Errorf("devices: %+v", ds)
	}
	// Bad torrent IDs are refused before reaching the engine.
	if w := remoteRequest(h, "POST", "/api/torrents/../remove", lan, host, got.Token, ""); w.Code == http.StatusOK {
		t.Errorf("bad hash accepted")
	}
	// Removing the device revokes its key.
	a.RemoveRemoteDevice(a.loadRemoteDevices()[0].ID)
	if w := remoteRequest(h, "GET", "/api/state", lan, host, got.Token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked key accepted: %d", w.Code)
	}

	// Rate limit: 10 failures from one address lock it out.
	for i := 0; i < 10; i++ {
		remoteRequest(h, "GET", "/api/state", "192.168.1.40:5000", host, "nope", "")
	}
	if w := remoteRequest(h, "GET", "/", "192.168.1.40:5000", host, "", ""); w.Code != http.StatusTooManyRequests {
		t.Errorf("not rate limited: %d", w.Code)
	}

	// 20 wrong guesses in total cancel the code, whatever the addresses.
	rs.failures = map[string][]time.Time{}
	rs.pairCode, rs.pairUntil, rs.pairFailures = "ABCDEFGH", time.Now().Add(time.Minute), 0
	for i := 0; i < 20; i++ {
		remoteRequest(h, "POST", "/api/pair", "10.0.0."+strconv.Itoa(i+1)+":5000", host, "", `{"code":"WRONGGGG"}`)
	}
	if w := remoteRequest(h, "POST", "/api/pair", "10.0.1.1:5000", host, "", `{"code":"ABCDEFGH"}`); w.Code != http.StatusForbidden {
		t.Errorf("code still valid after 20 wrong guesses: %d", w.Code)
	}
	if c := newPairCode(); len(c) != 8 || normalizePairCode(c) != c {
		t.Errorf("pair code %q", c)
	}
}
