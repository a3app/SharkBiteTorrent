//go:build darwin

package main

import (
	"net"
	"os/exec"
	"strings"
)

// defaultRoutes reads "netstat -rn -f inet": "default  192.168.1.1  UGScg  en0".
func defaultRoutes() []defaultRoute {
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return nil
	}
	var rs []defaultRoute
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "default" && net.ParseIP(f[1]) != nil {
			if local := ifaceIPv4(f[len(f)-1]); local != "" {
				rs = append(rs, defaultRoute{localIP: local, gateway: f[1]})
			}
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

// gatewayMAC reads "arp -n 192.168.1.1": "? (192.168.1.1) at aa:bb:cc:dd:ee:ff on en0 …".
func gatewayMAC(_, gateway string) string {
	out, err := exec.Command("arp", "-n", gateway).Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	for i := range f {
		if f[i] == "at" && i+1 < len(f) {
			return f[i+1]
		}
	}
	return ""
}
