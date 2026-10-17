//go:build windows

package main

import (
	"net"
	"os/exec"
	"strings"
	"syscall"

	"sharkbite/internal/systool"
)

// defaultRoutes reads IPv4 default routes from "route print -4": lines like
// "0.0.0.0  0.0.0.0  192.168.4.1  192.168.4.111  25".
func defaultRoutes() []defaultRoute {
	out, err := hiddenCommand(sysPath("route"), "print", "-4").Output()
	if err != nil {
		return nil
	}
	var rs []defaultRoute
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" && net.ParseIP(f[2]) != nil && net.ParseIP(f[3]) != nil {
			rs = append(rs, defaultRoute{localIP: f[3], gateway: f[2]})
		}
	}
	return rs
}

// gatewayMAC looks the router up in the ARP table of that interface:
// "  192.168.4.1           aa-bb-cc-dd-ee-ff     dynamic".
func gatewayMAC(localIP, gateway string) string {
	out, err := hiddenCommand(sysPath("arp"), "-a", "-N", localIP).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == gateway {
			return f[1]
		}
	}
	return ""
}

// hiddenCommand runs a console tool without flashing a window.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

// sysPath is where a Windows tool lives (System32), so PATH can't swap it.
func sysPath(name string) string {
	if p, ok := systool.Find(name); ok {
		return p
	}
	return name + ".exe-not-found"
}
