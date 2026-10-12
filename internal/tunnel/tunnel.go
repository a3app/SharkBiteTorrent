// Package tunnel runs a WireGuard client with a userspace TCP/IP stack inside
// this process. Only connections made through a Tunnel use the VPN; the rest
// of the PC is unaffected, and if the tunnel is down there is no fallback path.
package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Config is a generic WireGuard peer config, so non-WARP providers can be
// plugged in later.
type Config struct {
	PrivateKey    string   // base64
	PeerPublicKey string   // base64
	Endpoint      string   // host:port
	Addresses     []string // interface addresses, with or without /prefix
	DNS           []string
	MTU           int
}

type Tunnel struct {
	dev  *device.Device
	tnet *netstack.Net
}

func Start(cfg Config) (*Tunnel, error) {
	var addrs []netip.Addr
	for _, a := range cfg.Addresses {
		if a == "" {
			continue
		}
		a, _, _ = strings.Cut(a, "/")
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("address %q: %w", a, err)
		}
		addrs = append(addrs, ip)
	}
	var dns []netip.Addr
	for _, d := range cfg.DNS {
		ip, err := netip.ParseAddr(d)
		if err != nil {
			return nil, fmt.Errorf("dns %q: %w", d, err)
		}
		dns = append(dns, ip)
	}
	mtu := cfg.MTU
	if mtu == 0 {
		mtu = 1280
	}

	tdev, tnet, err := netstack.CreateNetTUN(addrs, dns, mtu)
	if err != nil {
		return nil, fmt.Errorf("create netstack: %w", err)
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wg: "))

	privHex, err := b64ToHex(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	peerHex, err := b64ToHex(cfg.PeerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("peer key: %w", err)
	}
	ipc := fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=%s\npersistent_keepalive_interval=25\nallowed_ip=0.0.0.0/0\nallowed_ip=::/0\n",
		privHex, peerHex, cfg.Endpoint)
	if err := dev.IpcSet(ipc); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure wireguard: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring up wireguard: %w", err)
	}
	return &Tunnel{dev: dev, tnet: tnet}, nil
}

func (t *Tunnel) Close() {
	t.dev.Close()
}

// Net exposes the userspace stack for dialing/listening (TCP and UDP).
func (t *Tunnel) Net() *netstack.Net { return t.tnet }

// ListenUDP4 opens an IPv4 UDP socket on an ephemeral port inside the tunnel.
// (netstack panics if the local address is left empty.)
func (t *Tunnel) ListenUDP4() (net.PacketConn, error) {
	return t.tnet.ListenUDPAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), 0))
}

func (t *Tunnel) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return t.tnet.DialContext(ctx, network, addr)
}

// HTTPClient returns a client whose traffic (including DNS) goes through the tunnel.
func (t *Tunnel) HTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{DialContext: t.tnet.DialContext},
	}
}

// Trace holds the fields from Cloudflare's /cdn-cgi/trace we care about.
type Trace struct {
	IP       string `json:"ip"`
	Location string `json:"location"`
	Colo     string `json:"colo"`
	Warp     string `json:"warp"`
}

// CheckTrace fetches Cloudflare's trace page through the tunnel, which
// reports the exit IP and whether WARP is active.
func (t *Tunnel) CheckTrace(ctx context.Context) (*Trace, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var tr Trace
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			tr.IP = v
		case "loc":
			tr.Location = v
		case "colo":
			tr.Colo = v
		case "warp":
			tr.Warp = v
		}
	}
	return &tr, nil
}

func b64ToHex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	if len(b) != 32 {
		return "", fmt.Errorf("want 32 bytes, got %d", len(b))
	}
	return hex.EncodeToString(b), nil
}
