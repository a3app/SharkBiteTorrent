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
	PresharedKey  string   // base64, optional
	Endpoint      string   // host:port
	Addresses     []string // interface addresses, with or without /prefix
	DNS           []string
	MTU           int
}

type Tunnel struct {
	dev      *device.Device
	tnet     *netstack.Net
	dns      string     // host:port of the DNS server reached through the tunnel
	v4       netip.Addr // our IPv4 address inside the tunnel
	v6       netip.Addr // our IPv6 address inside the tunnel, if any
	endpoint string     // the WireGuard server, ip:port
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
		dev.Close()
		return nil, fmt.Errorf("private key: %w", err)
	}
	peerHex, err := b64ToHex(cfg.PeerPublicKey)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("peer key: %w", err)
	}
	endpoint, err := resolveEndpoint(cfg.Endpoint)
	if err != nil {
		dev.Close()
		return nil, err
	}
	var ipc strings.Builder
	fmt.Fprintf(&ipc, "private_key=%s\npublic_key=%s\n", privHex, peerHex)
	if cfg.PresharedKey != "" {
		pskHex, err := b64ToHex(cfg.PresharedKey)
		if err != nil {
			dev.Close()
			return nil, fmt.Errorf("preshared key: %w", err)
		}
		fmt.Fprintf(&ipc, "preshared_key=%s\n", pskHex)
	}
	fmt.Fprintf(&ipc, "endpoint=%s\npersistent_keepalive_interval=25\nallowed_ip=0.0.0.0/0\nallowed_ip=::/0\n", endpoint)
	if err := dev.IpcSet(ipc.String()); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure wireguard: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring up wireguard: %w", err)
	}
	t := &Tunnel{dev: dev, tnet: tnet, dns: "1.1.1.1:53", endpoint: endpoint}
	for _, a := range addrs {
		if a.Is4() && !t.v4.IsValid() {
			t.v4 = a
		}
		if a.Is6() && !t.v6.IsValid() {
			t.v6 = a
		}
	}
	if len(dns) > 0 {
		t.dns = netip.AddrPortFrom(dns[0], 53).String()
	}
	return t, nil
}

// resolveEndpoint turns host:port into ip:port. It deliberately uses a fresh
// resolver (the OS one) rather than net.DefaultResolver, which the engine
// points at a previous tunnel.
func resolveEndpoint(endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return endpoint, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := (&net.Resolver{}).LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("resolve VPN endpoint %s: %v", host, err)
	}
	ip := ips[0]
	for _, a := range ips { // prefer IPv4, which works on more networks
		if a.Is4() {
			ip = a
			break
		}
	}
	return net.JoinHostPort(ip.Unmap().String(), port), nil
}

func (t *Tunnel) Close() {
	t.dev.Close()
}

// DNSServer is the ip:port of the DNS server used inside the tunnel.
func (t *Tunnel) DNSServer() string { return t.dns }

// Endpoint is the WireGuard server the tunnel talks to (ip:port).
func (t *Tunnel) Endpoint() string { return t.endpoint }

// ListenTCP4 listens for inbound connections on port inside the tunnel. Only
// useful with providers that forward a port to you.
func (t *Tunnel) ListenTCP4(port int) (net.Listener, error) {
	return t.tnet.ListenTCPAddrPort(netip.AddrPortFrom(t.localV4(), uint16(port)))
}

// localV4 is the tunnel's own IPv4 address. Sockets must bind to it: netstack
// has no route for packets sent from a socket bound to 0.0.0.0, so writes
// fail with "no route to host" (this silently broke DHT and UDP trackers).
func (t *Tunnel) localV4() netip.Addr {
	if t.v4.IsValid() {
		return t.v4
	}
	return netip.IPv4Unspecified()
}

// Net exposes the userspace stack for dialing/listening (TCP and UDP).
func (t *Tunnel) Net() *netstack.Net { return t.tnet }

// ListenUDP4 opens an IPv4 UDP socket on an ephemeral port inside the tunnel,
// bound to the tunnel's address (see localV4).
func (t *Tunnel) ListenUDP4() (net.PacketConn, error) {
	return t.tnet.ListenUDPAddrPort(netip.AddrPortFrom(t.localV4(), 0))
}

// ListenUDP opens a UDP socket for network "udp6" (bound to the tunnel's
// IPv6 address) or anything else (IPv4). A tunnel without IPv6 (PIA, many
// WireGuard providers) gets a socket that can't send: the torrent library
// panics if opening one fails (an udp6:// tracker crashed the app at every
// start), and this way those trackers just never answer.
func (t *Tunnel) ListenUDP(network string) (net.PacketConn, error) {
	if network == "udp6" {
		if !t.v6.IsValid() {
			return newNoRoutePacketConn(), nil
		}
		return t.tnet.ListenUDPAddrPort(netip.AddrPortFrom(t.v6, 0))
	}
	return t.ListenUDP4()
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
	return fetchTrace(ctx, t.HTTPClient())
}

// DirectTrace fetches the trace over the normal (non-VPN) connection, so the
// caller can confirm the tunnel's exit IP differs from the real one.
func DirectTrace(ctx context.Context) (*Trace, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, Resolver: &net.Resolver{}}
	return fetchTrace(ctx, &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true},
	})
}

func fetchTrace(ctx context.Context, hc *http.Client) (*Trace, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Cloudflare trace: HTTP %d", resp.StatusCode)
	}
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
