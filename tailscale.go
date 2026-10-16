package main

import (
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Tailscale access for the remote control (opt-in): the server also listens
// on this computer's Tailscale address and only answers other Tailscale
// addresses there. Tailscale encrypts that traffic end to end and only the
// user's own devices (and any they shared their network with) can reach it.

// tailscaleRange is the range Tailscale assigns addresses from (100.64/10,
// "carrier-grade NAT"). Some internet providers use it on ordinary WAN
// connections too, so an address in it only counts as Tailscale if
// Tailscale itself says so, or it's on the adapter Tailscale creates.
var tailscaleRange = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

func isTailscaleAddr(ip net.IP) bool { return ip != nil && tailscaleRange.Contains(ip) }

// tailscaleIP returns this computer's Tailscale IPv4 address, or "" if
// Tailscale isn't installed and connected.
func tailscaleIP() string {
	local := map[string]bool{}
	byIface := ""
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !isTailscaleAddr(n.IP) {
				continue
			}
			local[n.IP.String()] = true
			if strings.Contains(strings.ToLower(ifc.Name), "tailscale") {
				byIface = n.IP.String()
			}
		}
	}
	if len(local) == 0 {
		return ""
	}
	// Ask Tailscale (works on every platform, including macOS where its
	// adapter is just "utunN"), and only accept an address this computer has.
	if out, err := tailscaleCommand("ip", "-4").Output(); err == nil {
		for _, line := range strings.Fields(string(out)) {
			if local[line] {
				return line
			}
		}
	}
	return byIface
}

func tailscaleCommand(args ...string) *exec.Cmd {
	name := "tailscale"
	if _, err := exec.LookPath(name); err != nil {
		switch runtime.GOOS {
		case "windows":
			name = filepath.Join(`C:\Program Files\Tailscale`, "tailscale.exe")
		case "darwin":
			name = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		}
	}
	return hiddenCommand(name, args...)
}
