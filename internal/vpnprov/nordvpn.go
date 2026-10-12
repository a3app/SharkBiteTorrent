package vpnprov

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"sharkbite/internal/tunnel"
)

// NordVPN: the user creates an access token in their Nord Account dashboard
// (Services → NordVPN → Manual setup / Access token). The token unlocks the
// account's NordLynx (WireGuard) private key; servers come from the public
// recommendations API, filtered to P2P servers.
type nordvpn struct{}

var nordAPI = "https://api.nordvpn.com"

func init() { register(nordvpn{}) }

func (nordvpn) Info() Info {
	return Info{
		ID:   "nordvpn",
		Name: "NordVPN",
		Fields: []Field{{Key: "token", Label: "Access token", Kind: "password",
			Help: "Create one at my.nordaccount.com → NordVPN → Access token (or “Set up NordVPN manually”)."}},
	}
}

func (nordvpn) Login(ctx context.Context, creds map[string]string) (*Account, error) {
	token := strings.TrimSpace(creds["token"])
	if token == "" {
		return nil, errors.New("enter your NordVPN access token")
	}
	var r struct {
		PrivateKey string `json:"nordlynx_private_key"`
	}
	err := getJSON(ctx, httpClient(), nordAPI+"/v1/users/services/credentials", &r, func(req *http.Request) {
		req.SetBasicAuth("token", token)
	})
	if statusOf(err) == http.StatusUnauthorized {
		return nil, errors.New("NordVPN rejected the access token (expired or mistyped?)")
	}
	if err != nil {
		return nil, fmt.Errorf("NordVPN: %w", err)
	}
	if r.PrivateKey == "" {
		return nil, errors.New("NordVPN: account has no NordLynx key (is the subscription active?)")
	}
	return &Account{
		Provider: "nordvpn",
		Display:  "Token " + mask(token),
		Data:     map[string]string{"token": token, "privateKey": r.PrivateKey},
	}, nil
}

func (nordvpn) Logout(context.Context, *Account) error { return nil }

type nordCountry struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
}

func (nordvpn) countries(ctx context.Context) ([]nordCountry, error) {
	var cs []nordCountry
	if err := getJSON(ctx, httpClient(), nordAPI+"/v1/servers/countries", &cs); err != nil {
		return nil, fmt.Errorf("NordVPN countries: %w", err)
	}
	return cs, nil
}

func (n nordvpn) Regions(ctx context.Context) ([]Region, error) {
	cs, err := n.countries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Region, len(cs))
	for i, c := range cs {
		out[i] = Region{ID: c.Code, Name: c.Name}
	}
	return out, nil
}

func (n nordvpn) Connect(ctx context.Context, acc *Account, region, home string) (*Connection, error) {
	code := region
	if code == "" {
		code = home
	}
	q := url.Values{
		"filters[servers_technologies][identifier]": {"wireguard_udp"},
		"filters[servers_groups][identifier]":       {"legacy_p2p"},
		"limit":                                     {"5"},
	}
	if code != "" {
		cs, err := n.countries(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if strings.EqualFold(c.Code, code) {
				q.Set("filters[country_id]", strconv.Itoa(c.ID))
			}
		}
	}
	var servers []struct {
		Hostname  string `json:"hostname"`
		Station   string `json:"station"`
		Locations []struct {
			Country struct {
				Name string `json:"name"`
			} `json:"country"`
		} `json:"locations"`
		Technologies []struct {
			Identifier string `json:"identifier"`
			Metadata   []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"metadata"`
		} `json:"technologies"`
	}
	if err := getJSON(ctx, httpClient(), nordAPI+"/v1/servers/recommendations?"+q.Encode(), &servers); err != nil {
		return nil, fmt.Errorf("NordVPN servers: %w", err)
	}
	for _, s := range servers { // sorted by load; take the first usable one
		var pubKey string
		for _, t := range s.Technologies {
			if t.Identifier != "wireguard_udp" {
				continue
			}
			for _, m := range t.Metadata {
				if m.Name == "public_key" {
					pubKey = m.Value
				}
			}
		}
		if pubKey == "" || s.Station == "" {
			continue
		}
		where := ""
		if len(s.Locations) > 0 {
			where = " (" + s.Locations[0].Country.Name + ")"
		}
		return &Connection{
			Server: strings.TrimSuffix(s.Hostname, ".nordvpn.com") + where,
			Config: tunnel.Config{
				PrivateKey:    acc.Data["privateKey"],
				PeerPublicKey: pubKey,
				Endpoint:      s.Station + ":51820",
				Addresses:     []string{"10.5.0.2/32"},
				DNS:           []string{"103.86.96.100", "103.86.99.100"},
			},
		}, nil
	}
	return nil, errors.New("NordVPN has no P2P WireGuard servers for that location")
}
