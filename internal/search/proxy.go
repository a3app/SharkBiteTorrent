package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Proxy is the app's local SOCKS5 proxy, which leads into the VPN tunnel.
type Proxy struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ErrManualProxy means the server's proxy setting can't be changed through
// its API (Jackett keeps it behind the dashboard login), so the user has to
// enter the values themselves.
var ErrManualProxy = errors.New("this server's proxy has to be set in its own settings page")

// SetProxy points the server's outgoing traffic at p (enable) or turns its
// proxy off again, but only if it is still pointing at p.
//
// Prowlarr: Settings → General → Proxy, through /api/v1/config/host. The
// whole resource is sent back as read, as Prowlarr's own settings page does,
// so fields we don't touch (including login settings) stay as they were.
func SetProxy(ctx context.Context, c Config, p Proxy, enable bool) error {
	if c.Kind != Prowlarr {
		return ErrManualProxy
	}
	var host map[string]any
	if err := c.apiJSON(ctx, http.MethodGet, "/api/v1/config/host", nil, &host); err != nil {
		return err
	}
	id, ok := host["id"].(float64)
	if !ok {
		return errors.New("Prowlarr returned unexpected settings")
	}
	ours := host["proxyHostname"] == p.Host && host["proxyPort"] == float64(p.Port)
	if enable {
		host["proxyEnabled"] = true
		host["proxyType"] = "socks5"
		host["proxyHostname"] = p.Host
		host["proxyPort"] = p.Port
		host["proxyUsername"] = p.Username
		host["proxyPassword"] = p.Password
		// Keeps Prowlarr talking directly to Sonarr/Radarr and FlareSolverr on
		// this computer or the home network.
		host["proxyBypassLocalAddresses"] = true
	} else {
		if !ours || host["proxyEnabled"] != true {
			return nil // the user changed it themselves; leave it alone
		}
		host["proxyEnabled"] = false
	}
	return c.apiJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/config/host/%d", int(id)), host, nil)
}

// ProxyEnabled reports whether the server's proxy currently points at p.
func ProxyEnabled(ctx context.Context, c Config, p Proxy) (bool, error) {
	if c.Kind != Prowlarr {
		return false, ErrManualProxy
	}
	var host map[string]any
	if err := c.apiJSON(ctx, http.MethodGet, "/api/v1/config/host", nil, &host); err != nil {
		return false, err
	}
	return host["proxyEnabled"] == true && host["proxyHostname"] == p.Host && host["proxyPort"] == float64(p.Port), nil
}

func (c Config) apiJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %w", c.URL, hideURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("Prowlarr: HTTP %d %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
