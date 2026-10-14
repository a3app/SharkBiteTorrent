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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

var httpClient = &http.Client{Timeout: 90 * time.Second}

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
		return err
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

// Fetch turns a result into something the engine can add: its magnet link,
// or a .torrent saved to a temporary file (the caller removes it). Download
// links go through the user's server, which may answer with a .torrent or
// redirect to a magnet link.
func Fetch(ctx context.Context, r Result) (source string, tempFile string, err error) {
	if r.Magnet != "" {
		return r.Magnet, "", nil
	}
	if r.Download == "" {
		return "", "", errors.New("this result has no download link")
	}
	var magnet string
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Download, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download failed: %w", err)
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
	var dirs []string
	switch runtime.GOOS {
	case "windows":
		dirs = []string{os.Getenv("ProgramData")}
	default:
		if home, err := os.UserHomeDir(); err == nil {
			dirs = []string{filepath.Join(home, ".config")}
		}
	}
	var out []Config
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(d, "Prowlarr", "config.xml")); err == nil {
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
			}
		}
		if b, err := os.ReadFile(filepath.Join(d, "Jackett", "ServerConfig.json")); err == nil {
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
			}
		}
	}
	return out
}
