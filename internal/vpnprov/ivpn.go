package vpnprov

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"sharkbite/internal/tunnel"
)

// IVPN: account ID login creates a session and registers our WireGuard key,
// as IVPN's open-source desktop client does (github.com/ivpn/desktop-app,
// daemon/api). Supports 2FA; captcha challenges are not supported.
type ivpn struct{}

var ivpnAPI = "https://api.ivpn.net"

func init() { register(ivpn{}) }

const (
	ivpnOK            = 200
	ivpnSessionLimit  = 602
	ivpnNotActive     = 702
	ivpnCaptcha       = 70001
	ivpnNeed2FA       = 70011
	ivpnBad2FA        = 70012
	ivpnDefaultWGPort = 2049
)

func (ivpn) Info() Info {
	return Info{
		ID:   "ivpn",
		Name: "IVPN",
		Fields: []Field{{Key: "account", Label: "Account ID", Kind: "password",
			Help: "Looks like i-XXXX-XXXX-XXXX."}},
		Note: "Uses one of your IVPN device slots; signing out frees it.",
	}
}

func (ivpn) post(ctx context.Context, path string, body any, out any) (int, string, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ivpnAPI+path, bytes.NewReader(data))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := do(req)
	if err != nil {
		return 0, "", fmt.Errorf("couldn't reach IVPN: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var st struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return 0, "", fmt.Errorf("IVPN: unexpected response (HTTP %d)", resp.StatusCode)
	}
	if out != nil && st.Status == ivpnOK {
		if err := json.Unmarshal(raw, out); err != nil {
			return 0, "", fmt.Errorf("IVPN: %w", err)
		}
	}
	return st.Status, st.Message, nil
}

func (v ivpn) Login(ctx context.Context, creds map[string]string) (*Account, error) {
	account := strings.TrimSpace(creds["account"])
	if account == "" {
		return nil, errors.New("enter your IVPN account ID")
	}
	priv, pub, err := newKeypair()
	if err != nil {
		return nil, err
	}
	req := map[string]any{"username": account, "wg_public_key": pub, "force": creds["force"] == "true"}
	if code := strings.TrimSpace(creds["code2fa"]); code != "" {
		req["confirmation"] = code
	}
	var r struct {
		Token     string `json:"token"`
		WireGuard struct {
			Status    int    `json:"status"`
			Message   string `json:"message"`
			IPAddress string `json:"ip_address"`
		} `json:"wireguard"`
	}
	status, msg, err := v.post(ctx, "/v4/session/new", req, &r)
	if err != nil {
		return nil, err
	}
	switch status {
	case ivpnOK:
	case ivpnNeed2FA, ivpnBad2FA:
		return nil, &NeedFieldError{
			Field:   Field{Key: "code2fa", Label: "2FA code", Kind: "text"},
			Message: "IVPN: enter the code from your authenticator app",
		}
	case ivpnSessionLimit:
		return nil, &NeedFieldError{
			Field:   Field{Key: "force", Label: "Sign out another device to make room", Kind: "checkbox"},
			Message: "IVPN: your account's device limit is reached",
		}
	case ivpnCaptcha:
		return nil, errors.New("IVPN wants a captcha after failed attempts; sign in once at ivpn.net, then try again")
	case ivpnNotActive:
		return nil, errors.New("IVPN: account is not active")
	default:
		return nil, fmt.Errorf("IVPN: %s", msg)
	}
	if r.WireGuard.Status != ivpnOK || r.WireGuard.IPAddress == "" {
		return nil, fmt.Errorf("IVPN: couldn't register WireGuard key: %s", r.WireGuard.Message)
	}
	return &Account{
		Provider: "ivpn",
		Display:  "Account " + mask(account),
		Data: map[string]string{
			"session": r.Token, "privateKey": priv, "address": r.WireGuard.IPAddress,
		},
	}, nil
}

func (v ivpn) Logout(ctx context.Context, acc *Account) error {
	_, _, err := v.post(ctx, "/v4/session/delete", map[string]string{"session_token": acc.Data["session"]}, nil)
	return err
}

type ivpnServers struct {
	WireGuard []struct {
		Gateway     string `json:"gateway"`
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
		City        string `json:"city"`
		Hosts       []struct {
			Hostname  string `json:"hostname"`
			Host      string `json:"host"`
			PublicKey string `json:"public_key"`
			LocalIP   string `json:"local_ip"`
		} `json:"hosts"`
	} `json:"wireguard"`
	Config struct {
		Ports struct {
			WireGuard []struct {
				Type string `json:"type"`
				Port int    `json:"port"`
			} `json:"wireguard"`
		} `json:"ports"`
	} `json:"config"`
}

func (ivpn) servers(ctx context.Context) (*ivpnServers, error) {
	var s ivpnServers
	if err := getJSON(ctx, httpClient(), ivpnAPI+"/v5/servers.json", &s); err != nil {
		return nil, fmt.Errorf("IVPN server list: %w", err)
	}
	return &s, nil
}

func (v ivpn) Regions(ctx context.Context) ([]Region, error) {
	s, err := v.servers(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Region
	for _, g := range s.WireGuard {
		if !seen[g.CountryCode] {
			seen[g.CountryCode] = true
			out = append(out, Region{ID: g.CountryCode, Name: g.Country})
		}
	}
	return out, nil
}

func (v ivpn) Connect(ctx context.Context, acc *Account, region, home string) (*Connection, error) {
	s, err := v.servers(ctx)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = home
	}
	type host struct{ name, ip, key, gw, where string }
	var all, inRegion []host
	for _, g := range s.WireGuard {
		for _, h := range g.Hosts {
			gw, _, _ := strings.Cut(h.LocalIP, "/")
			x := host{h.Hostname, h.Host, h.PublicKey, gw, g.City + ", " + g.Country}
			all = append(all, x)
			if strings.EqualFold(g.CountryCode, region) {
				inRegion = append(inRegion, x)
			}
		}
	}
	if len(inRegion) == 0 {
		inRegion = all
	}
	if len(inRegion) == 0 {
		return nil, errors.New("IVPN returned no servers")
	}
	h := pick(inRegion)
	port := ivpnDefaultWGPort
	found := false
	for _, p := range s.Config.Ports.WireGuard {
		if p.Port == ivpnDefaultWGPort {
			found = true
		}
	}
	if !found && len(s.Config.Ports.WireGuard) > 0 {
		port = s.Config.Ports.WireGuard[0].Port
	}
	dns := h.gw
	if dns == "" {
		dns = "172.16.0.1"
	}
	addr := acc.Data["address"]
	if !strings.Contains(addr, "/") {
		addr += "/32"
	}
	return &Connection{
		Server: strings.TrimSuffix(h.name, ".wg.ivpn.net") + " (" + h.where + ")",
		Config: tunnel.Config{
			PrivateKey:    acc.Data["privateKey"],
			PeerPublicKey: h.key,
			Endpoint:      fmt.Sprintf("%s:%d", h.ip, port),
			Addresses:     []string{addr},
			DNS:           []string{dns},
		},
	}, nil
}
