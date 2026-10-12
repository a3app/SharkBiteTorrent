package vpnprov

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"sharkbite/internal/tunnel"
)

// Mullvad: the account number is the only credential. Same flow as Mullvad's
// official mullvad-wg.sh script: register our public key with /wg, then pick a
// relay from the public relay list.
type mullvad struct{}

var mullvadAPI = "https://api.mullvad.net"

func init() { register(mullvad{}) }

func (mullvad) Info() Info {
	return Info{
		ID:   "mullvad",
		Name: "Mullvad",
		Fields: []Field{{Key: "account", Label: "Account number", Kind: "password",
			Help: "The 16-digit number you log in to mullvad.net with."}},
		Note: "Adds a device to your Mullvad account. You can remove it at mullvad.net if you sign out.",
	}
}

func (mullvad) Login(ctx context.Context, creds map[string]string) (*Account, error) {
	account := strings.ReplaceAll(strings.TrimSpace(creds["account"]), " ", "")
	if account == "" {
		return nil, fmt.Errorf("enter your account number")
	}
	priv, pub, err := newKeypair()
	if err != nil {
		return nil, err
	}
	form := url.Values{"account": {account}, "pubkey": {pub}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mullvadAPI+"/wg", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Mullvad: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Mullvad: %s", text)
	}
	// Response is "10.x.x.x/32,fc00:...:/128".
	addrs := strings.Split(text, ",")
	if len(addrs) == 0 || !strings.Contains(addrs[0], ".") {
		return nil, fmt.Errorf("Mullvad: unexpected response %q", text)
	}
	return &Account{
		Provider: "mullvad",
		Display:  "Account " + mask(account),
		Data: map[string]string{
			"account": account, "privateKey": priv, "addresses": text,
		},
	}, nil
}

func (mullvad) Logout(context.Context, *Account) error { return nil }

type mullvadRelays struct {
	Countries []struct {
		Name   string `json:"name"`
		Code   string `json:"code"`
		Cities []struct {
			Name   string `json:"name"`
			Relays []struct {
				Hostname  string `json:"hostname"`
				IPv4      string `json:"ipv4_addr_in"`
				PublicKey string `json:"public_key"`
			} `json:"relays"`
		} `json:"cities"`
	} `json:"countries"`
}

func (mullvad) relays(ctx context.Context) (*mullvadRelays, error) {
	var r mullvadRelays
	if err := getJSON(ctx, httpClient(), mullvadAPI+"/public/relays/wireguard/v1/", &r); err != nil {
		return nil, fmt.Errorf("Mullvad server list: %w", err)
	}
	return &r, nil
}

func (m mullvad) Regions(ctx context.Context) ([]Region, error) {
	r, err := m.relays(ctx)
	if err != nil {
		return nil, err
	}
	var out []Region
	for _, c := range r.Countries {
		out = append(out, Region{ID: c.Code, Name: c.Name})
	}
	return out, nil
}

func (m mullvad) Connect(ctx context.Context, acc *Account, region, home string) (*Connection, error) {
	r, err := m.relays(ctx)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = strings.ToLower(home)
	}
	type relay struct{ host, ip, key, where string }
	var all, inRegion []relay
	for _, c := range r.Countries {
		for _, city := range c.Cities {
			for _, rl := range city.Relays {
				x := relay{rl.Hostname, rl.IPv4, rl.PublicKey, city.Name + ", " + c.Name}
				all = append(all, x)
				if strings.EqualFold(c.Code, region) {
					inRegion = append(inRegion, x)
				}
			}
		}
	}
	if len(inRegion) == 0 {
		inRegion = all
	}
	if len(inRegion) == 0 {
		return nil, fmt.Errorf("Mullvad returned no servers")
	}
	s := pick(inRegion)
	return &Connection{
		Server: s.host + " (" + s.where + ")",
		Config: tunnel.Config{
			PrivateKey:    acc.Data["privateKey"],
			PeerPublicKey: s.key,
			Endpoint:      s.ip + ":51820",
			Addresses:     strings.Split(acc.Data["addresses"], ","),
			DNS:           []string{"10.64.0.1"},
		},
	}, nil
}
