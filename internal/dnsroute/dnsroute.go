// Package dnsroute decides where this process's default DNS lookups go
// (net.DefaultResolver, used by anything that doesn't bring its own
// resolver, such as the torrent library's tracker lookups).
//
// The resolver is installed once and never replaced, so nothing races on the
// global; its Dial follows the current mode:
//
//   - System: the computer's DNS servers (at startup, and with No VPN).
//   - Tunnel: the VPN's DNS server, while a VPN engine runs.
//   - None: nowhere, between a VPN engine closing and the next one starting,
//     so a lookup still in flight in the old engine can't reach the ISP.
//
// Code that deliberately goes direct uses its own net.Resolver{} instead.
package dnsroute

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

var (
	mu      sync.Mutex
	tunnel  func(ctx context.Context, network string) (net.Conn, error) // nil: not Tunnel mode
	none    bool
	allowed int // AllowSystem calls in progress
)

func init() {
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dial}
}

var errNone = errors.New("DNS is off while the VPN isn't connected")

func dial(ctx context.Context, network, addr string) (net.Conn, error) {
	mu.Lock()
	t, off := tunnel, none && allowed == 0
	mu.Unlock()
	switch {
	case t != nil:
		c, err := t(ctx, network)
		if err != nil {
			return nil, err
		}
		return &searchFilter{Conn: c, tcp: strings.HasPrefix(network, "tcp"), search: searchDomains()}, nil
	case off:
		return nil, errNone
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// UseTunnel sends lookups to the VPN's DNS server through dial.
func UseTunnel(dial func(ctx context.Context, network string) (net.Conn, error)) {
	mu.Lock()
	tunnel, none = dial, false
	mu.Unlock()
}

// UseNone stops all default lookups (a VPN engine has closed).
func UseNone() {
	mu.Lock()
	tunnel, none = nil, true
	mu.Unlock()
}

// UseSystem sends lookups to the computer's DNS servers (No VPN).
func UseSystem() {
	mu.Lock()
	tunnel, none = nil, false
	mu.Unlock()
}

// AllowSystem lets default lookups use the computer's DNS while a
// deliberately direct operation runs (WARP registration, whose library has
// no resolver setting) even in None mode; call the returned func after.
func AllowSystem() (done func()) {
	mu.Lock()
	allowed++
	mu.Unlock()
	return func() {
		mu.Lock()
		allowed--
		mu.Unlock()
	}
}

// searchFilter drops queries for names under the computer's DNS search
// domains (from /etc/resolv.conf). Go's resolver retries a failed name with
// each one appended ("tracker.example.org.corp.local"), which would tell the
// VPN's DNS server the local network's domain; such names can't resolve
// there anyway.
type searchFilter struct {
	net.Conn
	tcp    bool
	search []string
}

var errSearchDomain = errors.New("not sent: a local search domain")

func (f *searchFilter) Write(b []byte) (int, error) {
	msg := b
	if f.tcp && len(b) >= 2 {
		msg = b[2:] // length prefix
	}
	var p dnsmessage.Parser
	if _, err := p.Start(msg); err == nil {
		if q, err := p.Question(); err == nil {
			name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
			for _, d := range f.search {
				if name == d || strings.HasSuffix(name, "."+d) {
					return 0, errSearchDomain
				}
			}
		}
	}
	return f.Conn.Write(b)
}

var (
	searchOnce sync.Once
	searchList []string
)

// searchDomains reads the search and domain lines of /etc/resolv.conf (not
// used on Windows, where the file doesn't exist).
func searchDomains() []string {
	searchOnce.Do(func() {
		b, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			return
		}
		searchList = parseSearch(string(b))
	})
	return searchList
}

func parseSearch(conf string) []string {
	var out []string
	for _, line := range strings.Split(conf, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "search" && f[0] != "domain") {
			continue
		}
		for _, d := range f[1:] {
			if d = strings.ToLower(strings.Trim(d, ".")); d != "" {
				out = append(out, d)
			}
		}
	}
	return out
}
