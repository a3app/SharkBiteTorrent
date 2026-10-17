package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Tailscale access for the remote control (opt-in): the server also listens
// on this computer's Tailscale address. Everything about Tailscale comes from
// Tailscale's own CLI at its fixed install location (never a "tailscale"
// found on PATH, where user-writable folders could hold a stand-in):
//   - this computer's address and user (tailscale status --json), which must
//     also be an address this computer actually has;
//   - for every device that connects, who it is (tailscale whois --json).
//     Only devices signed in to the same Tailscale account as this computer
//     are let in: not other people's devices on a shared tailnet, nodes
//     shared in, or anything behind a subnet router. A 100.64/10 address
//     alone (which some ISPs and hotel networks use) proves nothing.

var tailscaleRange = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

func isTailscaleAddr(ip net.IP) bool { return ip != nil && tailscaleRange.Contains(ip) }

// tailscaleBinary is Tailscale's CLI at its standard install location.
func tailscaleBinary() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		candidates = []string{filepath.Join(pf, "Tailscale", "tailscale.exe")}
	case "darwin":
		candidates = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/usr/local/bin/tailscale"}
	default:
		candidates = []string{"/usr/bin/tailscale", "/usr/sbin/tailscale", "/usr/local/bin/tailscale", "/snap/bin/tailscale"}
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// tailscaleJSON runs the CLI with a short timeout and decodes its JSON.
func tailscaleJSON(v any, args ...string) error {
	bin := tailscaleBinary()
	if bin == "" {
		return errors.New("Tailscale isn't installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := hiddenCommand(bin, args...)
	out, err := runWithContext(ctx, cmd)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

type tailscaleSelf struct {
	IP     string
	UserID int64
}

var (
	tsMu       sync.Mutex
	tsSelfVal  tailscaleSelf
	tsSelfAt   time.Time
	tsWhoCache = map[string]tailscalePeer{}
)

// tailscaleSelfInfo returns this computer's Tailscale IPv4 address and user,
// cached for 20 seconds. IP is "" if Tailscale isn't running.
func tailscaleSelfInfo() tailscaleSelf {
	tsMu.Lock()
	if time.Since(tsSelfAt) < 20*time.Second {
		v := tsSelfVal
		tsMu.Unlock()
		return v
	}
	tsMu.Unlock()

	var st struct {
		BackendState string
		Self         struct {
			TailscaleIPs []string
			UserID       int64
		}
	}
	var self tailscaleSelf
	if tailscaleJSON(&st, "status", "--json") == nil && st.BackendState == "Running" {
		local := localAddrs()
		for _, a := range st.Self.TailscaleIPs {
			ip := net.ParseIP(a)
			if ip != nil && ip.To4() != nil && isTailscaleAddr(ip) && local[ip.String()] {
				self = tailscaleSelf{IP: ip.String(), UserID: st.Self.UserID}
			}
		}
	}
	tsMu.Lock()
	tsSelfVal, tsSelfAt = self, time.Now()
	tsMu.Unlock()
	return self
}

func tailscaleIP() string { return tailscaleSelfInfo().IP }

func localAddrs() map[string]bool {
	m := map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			m[n.IP.String()] = true
		}
	}
	return m
}

// tailscalePeer is who a connecting Tailscale device is.
type tailscalePeer struct {
	OK   bool   // same Tailscale account as this computer
	Name string // its Tailscale device name
	at   time.Time
}

// tailscaleWhois asks Tailscale who ip is, cached for 5 minutes.
func tailscaleWhois(ip string) tailscalePeer {
	tsMu.Lock()
	if p, ok := tsWhoCache[ip]; ok && time.Since(p.at) < 5*time.Minute {
		tsMu.Unlock()
		return p
	}
	tsMu.Unlock()

	var who struct {
		Node *struct {
			Name         string
			ComputedName string
			Hostinfo     struct{ Hostname string }
		}
		UserProfile *struct{ ID int64 }
	}
	p := tailscalePeer{at: time.Now()}
	self := tailscaleSelfInfo()
	if self.IP != "" && tailscaleJSON(&who, "whois", "--json", ip) == nil && who.Node != nil && who.UserProfile != nil {
		p.OK = who.UserProfile.ID == self.UserID
		p.Name = who.Node.ComputedName
		if p.Name == "" {
			p.Name = who.Node.Hostinfo.Hostname
		}
	}
	tsMu.Lock()
	if len(tsWhoCache) > 1024 {
		tsWhoCache = map[string]tailscalePeer{}
	}
	tsWhoCache[ip] = p
	tsMu.Unlock()
	return p
}
