package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sharkbite/internal/search"
	"sharkbite/internal/secret"
)

// Remote control: a small web page for phones on the home network, to add
// and manage torrents on this computer. It's off by default.
//
// Security model:
//   - It listens on the computer's own network (never inside the VPN
//     tunnel, which can be reachable from the internet through a forwarded
//     port) and only answers private addresses: 192.168.x.x, 10.x.x.x,
//     172.16–31.x.x, IPv6 ULA/link-local, and this computer itself.
//   - The Host header must be an IP address or "localhost", which stops DNS
//     rebinding (a web page making your browser talk to it under its own
//     domain name).
//   - A phone pairs by scanning a QR code with a one-time code (5 minutes),
//     and gets a long random access key, sent as an Authorization header.
//     Pages on other sites can't read it or add that header, so they can't
//     act on your behalf. Keys are stored only as hashes, encrypted.
//   - Failed pairing/keys are rate-limited per address.
//   - Plain HTTP: the user chose this for the home network (a self-signed
//     HTTPS certificate would warn on every phone). Someone already on the
//     Wi-Fi could in principle capture a key.

//go:embed remote/*
var remoteFiles embed.FS

const (
	defaultRemotePort = 8732
	pairCodeLifetime  = 5 * time.Minute
	maxTorrentUpload  = 10 << 20
)

type remoteDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"tokenHash"`
	Created   int64  `json:"created"`
	LastSeen  int64  `json:"lastSeen"`
}

type remoteServer struct {
	srv  *http.Server
	port int

	mu           sync.Mutex
	pairCode     string
	pairUntil    time.Time
	pairFailures int                    // wrong codes against the current one, from anywhere
	failures     map[string][]time.Time // by client IP
}

// RemoteStatus is what Settings shows.
type RemoteStatus struct {
	Enabled bool               `json:"enabled"`
	Running bool               `json:"running"`
	Port    int                `json:"port"`
	URLs    []string           `json:"urls"`
	Error   string             `json:"error"`
	Devices []RemoteDeviceView `json:"devices"`
}

type RemoteDeviceView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"lastSeen"`
}

// RemotePairing is shown as a QR code in Settings.
type RemotePairing struct {
	URL     string `json:"url"`
	Code    string `json:"code"` // to type on the phone instead of scanning, e.g. "K7QM-4XRP"
	QR      string `json:"qr"`   // data: URL of a PNG
	Expires int64  `json:"expires"`
}

func (a *App) remotePath() string { return filepath.Join(a.configDir, "remote.json") }

func (a *App) loadRemoteDevices() []remoteDevice {
	var ds []remoteDevice
	if data, err := secret.ReadFile(a.remotePath()); err == nil {
		json.Unmarshal(data, &ds)
	}
	return ds
}

func (a *App) saveRemoteDevices(ds []remoteDevice) error {
	data, _ := json.Marshal(ds)
	return secret.WriteFile(a.remotePath(), data)
}

func (a *App) remotePort() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings.RemotePort > 0 {
		return a.settings.RemotePort
	}
	return defaultRemotePort
}

// startRemote starts the remote-control server if it's turned on.
func (a *App) startRemote() {
	a.mu.Lock()
	enabled := a.settings.RemoteEnabled
	a.mu.Unlock()
	if !enabled {
		return
	}
	port := a.remotePort()
	// The computer's own network, not the VPN tunnel (that's a separate,
	// in-process network stack the OS never sees).
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		a.mu.Lock()
		a.remoteErr = fmt.Sprintf("couldn't start on port %d: %v", port, err)
		a.mu.Unlock()
		return
	}
	rs := &remoteServer{port: port, failures: map[string][]time.Time{}}
	rs.srv = &http.Server{
		Handler:           a.remoteHandler(rs),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second, // searches can take a while
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	a.mu.Lock()
	a.remote, a.remoteErr = rs, ""
	a.mu.Unlock()
	go func() {
		if err := rs.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtime.LogWarningf(a.ctx, "remote control: %v", err)
		}
	}()
}

func (a *App) stopRemote() {
	a.mu.Lock()
	rs := a.remote
	a.remote = nil
	a.mu.Unlock()
	if rs != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		rs.srv.Shutdown(ctx)
		cancel()
	}
}

// --- request checks ---

// privateClient reports whether a request comes from the home network or
// this computer.
func privateClient(remoteAddr string) (string, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return "", false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host, false
	}
	return ip.String(), ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// hostIsAddress reports whether the Host header is an IP address or
// localhost: a browser only sends a domain name here if a page tricked it
// into talking to us under that name (DNS rebinding).
func hostIsAddress(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || net.ParseIP(h) != nil
}

func (rs *remoteServer) tooManyFailures(ip string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	recent := rs.failures[ip][:0]
	for _, t := range rs.failures[ip] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	rs.failures[ip] = recent
	return len(recent) >= 10
}

func (rs *remoteServer) recordFailure(ip string) {
	rs.mu.Lock()
	rs.failures[ip] = append(rs.failures[ip], time.Now())
	rs.mu.Unlock()
}

// Pairing codes are 8 characters a person can type: no 0/O, 1/I/L.
// 31^8 ≈ 8.5e11 possibilities against at most 10 guesses per address and 20
// in total before the code (valid 5 minutes) is cancelled.
const pairAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func newPairCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = pairAlphabet[int(b[i])%len(pairAlphabet)]
	}
	return string(b)
}

// normalizePairCode accepts what people type: lower case, spaces, dashes.
func normalizePairCode(s string) string {
	var out []byte
	for _, c := range strings.ToUpper(s) {
		if c < 128 && strings.ContainsRune(pairAlphabet, c) {
			out = append(out, byte(c))
		}
	}
	return string(out)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// device returns the paired device for the request's access key.
func (a *App) device(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return "", false
	}
	want := hashToken(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	ds := a.loadRemoteDevices()
	for i := range ds {
		if subtle.ConstantTimeCompare([]byte(ds[i].TokenHash), []byte(want)) == 1 {
			// Note when it was last used, at most once a minute.
			if now := time.Now().Unix(); now-ds[i].LastSeen > 60 {
				ds[i].LastSeen = now
				a.saveRemoteDevices(ds)
			}
			return ds[i].ID, true
		}
	}
	return "", false
}

// --- handlers ---

var hashPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (a *App) remoteHandler(rs *remoteServer) http.Handler {
	mux := http.NewServeMux()
	page, _ := fs.Sub(remoteFiles, "remote")
	mux.Handle("GET /", http.FileServer(http.FS(page)))

	mux.HandleFunc("POST /api/pair", func(w http.ResponseWriter, r *http.Request) {
		ip, _ := privateClient(r.RemoteAddr)
		var req struct{ Code, Name string }
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		code := normalizePairCode(req.Code)
		rs.mu.Lock()
		ok := rs.pairCode != "" && time.Now().Before(rs.pairUntil) &&
			subtle.ConstantTimeCompare([]byte(code), []byte(rs.pairCode)) == 1
		if ok {
			rs.pairCode = "" // one use only
		} else if rs.pairCode != "" {
			// Besides the per-address limit: too many wrong guesses from
			// anywhere (e.g. many addresses) cancel the code altogether.
			if rs.pairFailures++; rs.pairFailures >= 20 {
				rs.pairCode = ""
			}
		}
		rs.mu.Unlock()
		if !ok {
			rs.recordFailure(ip)
			http.Error(w, "this pairing code is wrong or expired; show a new QR code in Settings → Remote control", http.StatusForbidden)
			return
		}
		token := randomString(32)
		name := strings.TrimSpace(req.Name)
		if len(name) > 60 {
			name = name[:60]
		}
		if name == "" {
			name = "Phone"
		}
		now := time.Now().Unix()
		a.mu.Lock()
		ds := append(a.loadRemoteDevices(), remoteDevice{ID: randomString(9), Name: name, TokenHash: hashToken(token), Created: now, LastSeen: now})
		err := a.saveRemoteDevices(ds)
		a.mu.Unlock()
		if err != nil {
			http.Error(w, "couldn't save the pairing", http.StatusInternalServerError)
			return
		}
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "remote-paired")
		}
		writeJSON(w, map[string]string{"token": token})
	})

	api := func(pattern string, h func(w http.ResponseWriter, r *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			ip, _ := privateClient(r.RemoteAddr)
			if _, ok := a.device(r); !ok {
				rs.recordFailure(ip)
				http.Error(w, "not paired", http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxTorrentUpload+1<<20)
			if err := h(w, r); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		})
	}
	api("GET /api/state", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, map[string]any{"vpn": a.GetVpnStatus(), "torrents": a.ListTorrents()})
		return nil
	})
	api("POST /api/magnet", func(w http.ResponseWriter, r *http.Request) error {
		var req struct{ Link string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return errors.New("bad request")
		}
		return a.AddMagnet(req.Link)
	})
	api("POST /api/torrent", func(w http.ResponseWriter, r *http.Request) error {
		f, _, err := r.FormFile("file")
		if err != nil {
			return errors.New("no file")
		}
		defer f.Close()
		tmp, err := os.CreateTemp("", "sharkbite-remote-*.torrent")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, err = io.Copy(tmp, io.LimitReader(f, maxTorrentUpload))
		tmp.Close()
		if err != nil {
			return err
		}
		return a.addSource(tmp.Name()) // keeps its own copy
	})
	api("POST /api/torrents/{hash}/{action}", func(w http.ResponseWriter, r *http.Request) error {
		hash := r.PathValue("hash")
		if !hashPattern.MatchString(hash) {
			return errors.New("bad torrent")
		}
		switch r.PathValue("action") {
		case "pause":
			return a.Pause(hash)
		case "resume":
			return a.Resume(hash)
		case "remove":
			return a.Remove(hash, r.URL.Query().Get("files") == "1")
		}
		return errors.New("unknown action")
	})
	api("GET /api/search", func(w http.ResponseWriter, r *http.Request) error {
		rs, err := a.SearchTorrents(r.URL.Query().Get("q"), r.URL.Query().Get("cat"))
		if err != nil {
			return err
		}
		writeJSON(w, rs)
		return nil
	})
	api("POST /api/search/add", func(w http.ResponseWriter, r *http.Request) error {
		var res search.Result
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			return errors.New("bad request")
		}
		return a.AddSearchResult(res)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, ok := privateClient(r.RemoteAddr)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !hostIsAddress(r.Host) {
			http.Error(w, "use this computer's address, not a domain name", http.StatusMisdirectedRequest)
			return
		}
		if rs.tooManyFailures(ip) {
			http.Error(w, "too many failed attempts; try again in a few minutes", http.StatusTooManyRequests)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; manifest-src 'self'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// lanURLs lists this computer's home-network addresses, most likely first.
func lanURLs(port int) []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, "http://"+net.JoinHostPort(ipn.IP.String(), strconv.Itoa(port))+"/")
		}
	}
	rank := func(u string) int {
		switch {
		case strings.HasPrefix(u, "http://192.168."):
			return 0
		case strings.HasPrefix(u, "http://10."):
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// --- bound to the frontend ---

func (a *App) GetRemoteStatus() RemoteStatus {
	a.mu.Lock()
	st := RemoteStatus{Enabled: a.settings.RemoteEnabled, Running: a.remote != nil, Error: a.remoteErr}
	ds := a.loadRemoteDevices()
	a.mu.Unlock()
	st.Port = a.remotePort()
	if st.Running {
		st.URLs = lanURLs(st.Port)
	}
	st.Devices = []RemoteDeviceView{}
	for _, d := range ds {
		st.Devices = append(st.Devices, RemoteDeviceView{ID: d.ID, Name: d.Name, Created: d.Created, LastSeen: d.LastSeen})
	}
	return st
}

// SetRemote turns the remote control on or off and sets its port.
func (a *App) SetRemote(enabled bool, port int) (RemoteStatus, error) {
	if port == 0 {
		port = defaultRemotePort
	}
	if port < 1024 || port > 65535 {
		return a.GetRemoteStatus(), errors.New("use a port between 1024 and 65535")
	}
	a.stopRemote()
	a.mu.Lock()
	a.settings.RemoteEnabled, a.settings.RemotePort = enabled, port
	a.remoteErr = ""
	a.mu.Unlock()
	if err := a.writeSettings(); err != nil {
		return a.GetRemoteStatus(), err
	}
	a.startRemote()
	return a.GetRemoteStatus(), nil
}

// StartRemotePairing makes a one-time pairing code and its QR code.
func (a *App) StartRemotePairing() (RemotePairing, error) {
	a.mu.Lock()
	rs := a.remote
	a.mu.Unlock()
	if rs == nil {
		return RemotePairing{}, errors.New("turn the remote control on first")
	}
	urls := lanURLs(rs.port)
	if len(urls) == 0 {
		return RemotePairing{}, errors.New("this computer doesn't seem to be on a home network")
	}
	code := newPairCode()
	until := time.Now().Add(pairCodeLifetime)
	rs.mu.Lock()
	rs.pairCode, rs.pairUntil, rs.pairFailures = code, until, 0
	rs.mu.Unlock()
	// The code is in the #fragment: the browser doesn't send that to any
	// server or put it in logs; the page reads it and posts it once.
	u := urls[0] + "#pair=" + code
	png, err := qrcode.Encode(u, qrcode.Medium, 320)
	if err != nil {
		return RemotePairing{}, err
	}
	return RemotePairing{URL: u, Code: code[:4] + "-" + code[4:], QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), Expires: until.Unix()}, nil
}

func (a *App) RemoveRemoteDevice(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ds := a.loadRemoteDevices()
	out := ds[:0]
	for _, d := range ds {
		if d.ID != id {
			out = append(out, d)
		}
	}
	return a.saveRemoteDevices(out)
}
