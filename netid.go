package main

import (
	"net"
	"strings"
)

// lanNet is a real local network this computer is on: the address it has
// there and that network's router (default gateway), identified by the
// router's hardware (MAC) address. The remote control only listens on these
// and, once a phone has paired, only on the one it paired on.
//
// Interfaces without a default gateway that has a MAC address aren't real
// home networks: Docker/WSL/Hyper-V virtual adapters have no gateway, and
// overlay or point-to-point VPNs have no router MAC. They're never listed.
type lanNet struct {
	LocalIP    string `json:"localIP"`
	Gateway    string `json:"gateway"`
	GatewayMAC string `json:"gatewayMAC"`
}

// same reports whether two networks are the same home network: the same
// router. The computer's own address may change (DHCP) without the network
// changing.
func (n lanNet) same(o lanNet) bool {
	return n.GatewayMAC != "" && strings.EqualFold(n.GatewayMAC, o.GatewayMAC) && n.Gateway == o.Gateway
}

// defaultRoute is a default-route entry: the gateway and the local address
// of the interface it's reached through.
type defaultRoute struct{ localIP, gateway string }

// lanNetworks lists the real local networks this computer is on now.
func lanNetworks() []lanNet {
	var out []lanNet
	seen := map[string]bool{}
	for _, r := range defaultRoutes() {
		ip := net.ParseIP(r.localIP)
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() || seen[r.localIP] {
			continue
		}
		mac := normalizeMAC(gatewayMAC(r.localIP, r.gateway))
		if mac == "" || mac == "ff:ff:ff:ff:ff:ff" || mac == "00:00:00:00:00:00" {
			continue
		}
		seen[r.localIP] = true
		out = append(out, lanNet{LocalIP: r.localIP, Gateway: r.gateway, GatewayMAC: mac})
	}
	return out
}

func normalizeMAC(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", ":"))
	if hw, err := net.ParseMAC(s); err == nil {
		return hw.String()
	}
	return ""
}
