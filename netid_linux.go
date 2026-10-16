//go:build linux

package main

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"strings"
)

// defaultRoutes reads /proc/net/route (destination 00000000 = default).
func defaultRoutes() []defaultRoute {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil
	}
	var rs []defaultRoute
	for _, line := range strings.Split(string(data), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		gw, err := hex.DecodeString(f[2])
		if err != nil || len(gw) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, binary.LittleEndian.Uint32(gw))
		if local := ifaceIPv4(f[0]); local != "" {
			rs = append(rs, defaultRoute{localIP: local, gateway: ip.String()})
		}
	}
	return rs
}

func ifaceIPv4(name string) string {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

// gatewayMAC reads /proc/net/arp.
func gatewayMAC(_, gateway string) string {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == gateway {
			return f[3]
		}
	}
	return ""
}
