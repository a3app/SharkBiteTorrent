// Package search queries the user's own Prowlarr or Jackett server, which
// aggregates whichever torrent sites (indexers) the user has configured.
// The app ships no sites of its own.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	Prowlarr Kind = "prowlarr"
	Jackett  Kind = "jackett"
)

// Config is how to reach the server. APIKey is a secret; the app stores it
// encrypted.
type Config struct {
	Kind   Kind   `json:"kind"`
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type Result struct {
	Title     string `json:"title"`
	Size      int64  `json:"size"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
	Indexer   string `json:"indexer"`
	Published string `json:"published"` // RFC 3339
	Magnet    string `json:"magnet,omitempty"`
	Download  string `json:"download,omitempty"` // .torrent (or magnet redirect) via the server
	InfoURL   string `json:"infoUrl,omitempty"`
}

// Categories use the standard Newznab numbers both servers understand.
var Categories = map[string]int{
	"movies": 2000, "tv": 5000, "music": 3000, "software": 4000, "books": 7000,
}

// localTransport reaches the user's own server (see serverDial).
var localTransport = &http.Transport{
	DialContext:         serverDial,
	TLSHandshakeTimeout: 10 * time.Second,
}

var (
	tunnelMu   sync.Mutex
	tunnelDial Dialer
)

// SetTunnel sets how a server that isn't on this computer, the home network
// or Tailscale is reached: the app passes its VPN dialer (which connects
// directly only in No VPN mode). Until then such servers can't be reached.
func SetTunnel(d Dialer) {
	tunnelMu.Lock()
	tunnelDial = d
	tunnelMu.Unlock()
}

func viaTunnel(ctx context.Context, network, addr string) (net.Conn, error) {
	tunnelMu.Lock()
	d := tunnelDial
	tunnelMu.Unlock()
	if d == nil {
		return nil, errors.New("VPN not connected")
	}
	return d(ctx, network, addr)
}

var directDialer = &net.Dialer{Timeout: 10 * time.Second}

// serverDial connects to the user's Prowlarr/Jackett. One on this computer,
// the home network or Tailscale is reached directly (the VPN can't reach
// those), using the operating system's name resolution, since the VPN's DNS
// doesn't know "localhost" or home names. Anything else, such as a seedbox,
// goes through the VPN, and its name is never looked up outside it.
func serverDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if localIP(ip) {
			return directDialer.DialContext(ctx, network, addr)
		}
		return viaTunnel(ctx, network, addr)
	}
	if !localName(host) {
		return viaTunnel(ctx, network, addr)
	}
	ips, err := (&net.Resolver{}).LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error = fmt.Errorf("%s has no address", host)
	for _, ip := range ips {
		if !localIP(ip.IP) {
			return nil, fmt.Errorf("%s isn't on this computer or the home network; use its public name or address so it's reached through the VPN", host)
		}
	}
	for _, ip := range ips {
		c, err := directDialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

var tailscaleNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// localIP: this computer, the home network (private and link-local
// addresses) or Tailscale (100.64/10).
func localIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailscaleNet.Contains(ip)
}

// localName: names that only make sense on this computer or the home
// network (or Tailscale's MagicDNS), so the system resolver may look them up.
func localName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".ts.net"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

var httpClient = &http.Client{Timeout: 90 * time.Second, Transport: localTransport}

func (c Config) base() string { return strings.TrimRight(c.URL, "/") }

// Detect finds out whether url+key is a working Prowlarr or Jackett server.
func Detect(ctx context.Context, rawURL, key string) (Config, error) {
	cfg := Config{URL: strings.TrimRight(strings.TrimSpace(rawURL), "/"), APIKey: strings.TrimSpace(key)}
	if u, err := url.Parse(cfg.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return cfg, errors.New("enter the server address, e.g. http://localhost:9696")
	}
	// Prowlarr
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/v1/system/status", nil)
	req.Header.Set("X-Api-Key", cfg.APIKey)
	if resp, err := httpClient.Do(req); err == nil {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			cfg.Kind = Prowlarr
			return cfg, nil
		case http.StatusUnauthorized:
			return cfg, errors.New("Prowlarr rejected the API key (Settings → General in Prowlarr)")
		}
	}
	// Jackett
	q := url.Values{"apikey": {cfg.APIKey}, "configured": {"true"}}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/v2.0/indexers?"+q.Encode(), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return cfg, fmt.Errorf("couldn't reach %s: is Prowlarr or Jackett running?", cfg.URL)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		cfg.Kind = Jackett
		return cfg, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return cfg, errors.New("Jackett rejected the API key (shown at the top of Jackett's page)")
	}
	return cfg, fmt.Errorf("%s doesn't look like Prowlarr or Jackett (HTTP %d)", cfg.URL, resp.StatusCode)
}

// Search runs a query across all of the user's indexers. category is a key
// of Categories, or "" for everything. Results are sorted by seeders.
func Search(ctx context.Context, c Config, query, category string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("type something to search for")
	}
	var out []Result
	var err error
	switch c.Kind {
	case Prowlarr:
		out, err = searchProwlarr(ctx, c, query, Categories[category])
	case Jackett:
		out, err = searchJackett(ctx, c, query, Categories[category])
	default:
		return nil, errors.New("search isn't set up")
	}
	if err != nil {
		return nil, err
	}
	out = dedupe(out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	return out, nil
}

func getJSON(ctx context.Context, u string, header map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, val := range header {
		req.Header.Set(k, val)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return hideURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("search server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func searchProwlarr(ctx context.Context, c Config, query string, cat int) ([]Result, error) {
	q := url.Values{"query": {query}, "type": {"search"}, "limit": {"100"}}
	if cat > 0 {
		q.Set("categories", strconv.Itoa(cat))
	}
	var rs []struct {
		Title       string `json:"title"`
		Size        int64  `json:"size"`
		Seeders     *int   `json:"seeders"`
		Leechers    *int   `json:"leechers"`
		Indexer     string `json:"indexer"`
		PublishDate string `json:"publishDate"`
		MagnetURL   string `json:"magnetUrl"`
		DownloadURL string `json:"downloadUrl"`
		InfoURL     string `json:"infoUrl"`
		Protocol    string `json:"protocol"`
	}
	if err := getJSON(ctx, c.base()+"/api/v1/search?"+q.Encode(), map[string]string{"X-Api-Key": c.APIKey}, &rs); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(rs))
	for _, r := range rs {
		if r.Protocol != "" && r.Protocol != "torrent" {
			continue // Usenet results, if the user has those indexers too
		}
		out = append(out, Result{
			Title: r.Title, Size: r.Size, Seeders: deref(r.Seeders), Leechers: deref(r.Leechers),
			Indexer: r.Indexer, Published: r.PublishDate,
			Magnet: r.MagnetURL, Download: r.DownloadURL, InfoURL: r.InfoURL,
		})
	}
	return out, nil
}

func searchJackett(ctx context.Context, c Config, query string, cat int) ([]Result, error) {
	q := url.Values{"apikey": {c.APIKey}, "Query": {query}}
	if cat > 0 {
		q.Add("Category[]", strconv.Itoa(cat))
	}
	var resp struct {
		Results []struct {
			Title       string `json:"Title"`
			Size        int64  `json:"Size"`
			Seeders     *int   `json:"Seeders"`
			Peers       *int   `json:"Peers"`
			Tracker     string `json:"Tracker"`
			PublishDate string `json:"PublishDate"`
			MagnetURI   string `json:"MagnetUri"`
			Link        string `json:"Link"`
			Details     string `json:"Details"`
		} `json:"Results"`
	}
	if err := getJSON(ctx, c.base()+"/api/v2.0/indexers/all/results?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		seeds := deref(r.Seeders)
		out = append(out, Result{
			Title: r.Title, Size: r.Size, Seeders: seeds, Leechers: max(0, deref(r.Peers)-seeds),
			Indexer: r.Tracker, Published: r.PublishDate,
			Magnet: r.MagnetURI, Download: r.Link, InfoURL: r.Details,
		})
	}
	return out, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// Dialer opens a connection through the VPN tunnel.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// fetchTransport reaches the configured Prowlarr/Jackett with serverDial
// (directly only if it's local) and everything else through the VPN. A result's link, or a redirect
// from it, can point at the indexer site itself; those requests must not
// show the user's real IP.
func fetchTransport(c Config, tunnel Dialer) *http.Transport {
	server := ""
	if u, err := url.Parse(c.URL); err == nil {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		server = net.JoinHostPort(strings.ToLower(u.Hostname()), port)
	}
	direct := localTransport.DialContext
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, port, err := net.SplitHostPort(addr); err == nil && net.JoinHostPort(strings.ToLower(host), port) == server {
				return direct(ctx, network, addr)
			}
			if tunnel == nil {
				return nil, errors.New("VPN not connected")
			}
			return tunnel(ctx, network, addr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// Fetch turns a result into something the engine can add: its magnet link,
// or a .torrent saved to a temporary file (the caller removes it). Download
// links normally go through the user's server, which may answer with a
// .torrent or redirect to a magnet link. Only that server is contacted
// directly; any other host (a direct indexer link, or a redirect to one)
// goes through tunnel.
func Fetch(ctx context.Context, c Config, r Result, tunnel Dialer) (source string, tempFile string, err error) {
	if strings.HasPrefix(r.Magnet, "magnet:") {
		return r.Magnet, "", nil
	}
	// Prowlarr's magnetUrl is often its own http download link that redirects to the magnet.
	link := r.Download
	if link == "" {
		link = r.Magnet
	}
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return "", "", errors.New("this result has no download link")
	}
	var magnet string
	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: fetchTransport(c, tunnel),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// The previous URL may carry the server's API key (?apikey=);
			// it mustn't reach the indexer site as the Referer.
			req.Header.Del("Referer")
			if req.URL.Scheme == "magnet" {
				magnet = req.URL.String()
				return http.ErrUseLastResponse
			}
			if len(via) > 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", "", errors.New("this result has a bad download link")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download failed: %w", hideURL(err))
	}
	defer resp.Body.Close()
	if magnet != "" {
		return magnet, "", nil
	}
	if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "magnet:") {
		return loc, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", "", err
	}
	if s := strings.TrimSpace(string(data)); strings.HasPrefix(s, "magnet:") {
		return s, "", nil
	}
	if !bytes.HasPrefix(data, []byte("d")) { // .torrent files are bencoded dictionaries
		return "", "", errors.New("the site didn't return a torrent (it may need FlareSolverr or a login)")
	}
	f, err := os.CreateTemp("", "sharkbite-search-*.torrent")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", "", err
	}
	return f.Name(), f.Name(), nil
}

// LocalServers looks for Prowlarr or Jackett installed on this computer and
// returns their address and API key from their own settings files.
func LocalServers() []Config {
	var prowlarrFiles, jackettFiles []string
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("ProgramData"); d != "" {
			prowlarrFiles = []string{filepath.Join(d, "Prowlarr", "config.xml")}
			jackettFiles = []string{filepath.Join(d, "Jackett", "ServerConfig.json")}
		}
	default:
		if home, err := os.UserHomeDir(); err == nil {
			prowlarrFiles = []string{filepath.Join(home, ".config", "Prowlarr", "config.xml")}
			jackettFiles = []string{filepath.Join(home, ".config", "Jackett", "ServerConfig.json")}
		}
		if runtime.GOOS == "linux" {
			// Prowlarr installed as a system service (its install script, the
			// Arch/AUR packages) keeps its world-readable settings here.
			prowlarrFiles = append(prowlarrFiles, "/var/lib/prowlarr/config.xml")
		}
	}
	var out []Config
	for _, f := range prowlarrFiles {
		if b, err := os.ReadFile(f); err == nil {
			var x struct {
				Port    int    `xml:"Port"`
				URLBase string `xml:"UrlBase"`
				APIKey  string `xml:"ApiKey"`
			}
			if xml.Unmarshal(b, &x) == nil && x.APIKey != "" {
				port := x.Port
				if port == 0 {
					port = 9696
				}
				out = append(out, Config{Kind: Prowlarr, URL: fmt.Sprintf("http://localhost:%d%s", port, strings.TrimRight(x.URLBase, "/")), APIKey: x.APIKey})
				break
			}
		}
	}
	for _, f := range jackettFiles {
		if b, err := os.ReadFile(f); err == nil {
			var j struct {
				Port     int    `json:"Port"`
				APIKey   string `json:"APIKey"`
				BasePath string `json:"BasePathOverride"`
			}
			if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &j) == nil && j.APIKey != "" {
				port := j.Port
				if port == 0 {
					port = 9117
				}
				out = append(out, Config{Kind: Jackett, URL: fmt.Sprintf("http://localhost:%d%s", port, strings.TrimRight(j.BasePath, "/")), APIKey: j.APIKey})
				break
			}
		}
	}
	return out
}

// dedupe drops exact repeats: indexers sometimes list the same release more
// than once (same title and size), which only clutters the results.
func dedupe(rs []Result) []Result {
	seen := make(map[string]bool, len(rs))
	out := rs[:0]
	for _, r := range rs {
		k := r.Indexer + "\x00" + r.Title + "\x00" + strconv.FormatInt(r.Size, 10)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

// hideURL drops the request URL from an HTTP error, since it can carry the API key.
func hideURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
