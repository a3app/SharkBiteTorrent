// Package warp registers a free Cloudflare WARP device and produces the
// WireGuard parameters needed to connect to it (same flow as wgcf).
package warp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/ViRb3/wgcf/v2/cloudflare"
	"github.com/ViRb3/wgcf/v2/wireguard"

	"sharkbite/internal/dnsroute"

	"sharkbite/internal/secret"
)

// Account is the persisted WARP registration.
type Account struct {
	ID            string `json:"id"`
	Token         string `json:"token"`
	PrivateKey    string `json:"private_key"` // base64
	PeerPublicKey string `json:"peer_public_key"`
	Endpoint      string `json:"endpoint"` // host:port
	AddressV4     string `json:"address_v4"`
	AddressV6     string `json:"address_v6"`
	ClientID      string `json:"client_id"` // base64, used for "reserved" bytes
}

// LoadOrRegister returns the account stored (encrypted) at path, registering
// a new one if the file doesn't exist.
func LoadOrRegister(path string) (*Account, error) {
	if data, err := secret.ReadFile(path); err == nil {
		var acc Account
		if err := json.Unmarshal(data, &acc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return &acc, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return Reregister(path)
}

// Reregister registers a new WARP device and saves it at path, replacing any
// previous one. The old file is only overwritten once registration succeeds.
func Reregister(path string) (*Account, error) {
	acc, err := Register()
	if err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(acc, "", "  ")
	if err := secret.WriteFile(path, data); err != nil {
		return nil, err
	}
	return acc, nil
}

var directOnce sync.Once

// directDNS makes wgcf's connections (a deliberate direct call: it's how a
// WARP tunnel comes to exist) look Cloudflare's API up with the computer's
// DNS. wgcf's dialer has no resolver setting, but it passes the request's
// context on to the lookup, which dnsroute.WithSystem marks; nothing else
// in the process is affected.
func directDNS() {
	t := cloudflare.DefaultTransport
	dial := t.DialTLSContext
	t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dial(dnsroute.WithSystem(ctx), network, addr)
	}
}

// Register creates a new free WARP device with a freshly generated key.
// The API call goes through wgcf, which mimics the official app's TLS
// fingerprint and headers (Cloudflare rejects plain Go clients with 429).
func Register() (*Account, error) {
	priv, err := wireguard.NewPrivateKey()
	if err != nil {
		return nil, err
	}
	directOnce.Do(directDNS)
	r, err := cloudflare.Register(priv.Public(), "PC")
	if err != nil {
		return nil, fmt.Errorf("warp register: %w", err)
	}
	if r.Config == nil || len(r.Config.Peers) == 0 {
		return nil, fmt.Errorf("warp register: no peers in response")
	}
	peer := r.Config.Peers[0]
	// Prefer the literal IPv4 endpoint: the hostname form is blocked by some
	// DNS resolvers, and a literal IP avoids a DNS lookup outside the tunnel.
	ep := peer.Endpoint
	ep.Host = nil
	endpoint, err := cloudflare.WireGuardEndpoint(ep)
	if err != nil {
		return nil, fmt.Errorf("warp register: %w", err)
	}

	return &Account{
		ID:            r.Id,
		Token:         r.Token,
		PrivateKey:    priv.String(),
		PeerPublicKey: peer.PublicKey,
		Endpoint:      endpoint,
		AddressV4:     r.Config.Interface.Addresses.V4,
		AddressV6:     r.Config.Interface.Addresses.V6,
		ClientID:      r.Config.ClientId,
	}, nil
}
