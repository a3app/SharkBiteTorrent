//go:build linux

package main

import (
	"os"
	"reflect"
	"testing"
)

// testdata/nft-linux.json is real `nft -j list table` output of nftRules,
// after this traffic from the watched cgroup: 3 UDP packets to the "VPN
// endpoint" 10.9.9.2:51820, a TCP connection attempt to 10.9.9.3:443, a DNS
// query to the 127.0.0.53 stub, a UDP packet to 127.0.0.1:9 and a ping to
// 10.9.9.5.
func TestParseAndClassify(t *testing.T) {
	data, err := os.ReadFile("testdata/nft-linux.json")
	if err != nil {
		t.Fatal(err)
	}
	flows, total, err := parseNft(data)
	if err != nil {
		t.Fatal(err)
	}
	list, counted, tunnel := classify(flows, policy{local: map[string]bool{"10.9.9.1": true}, endpoint: "10.9.9.2:51820"})
	if counted != total || tunnel != 3 {
		t.Errorf("counted %d of %d packets, tunnel %d", counted, total, tunnel)
	}
	want := []string{
		"DNS lookup outside the tunnel (to 127.0.0.53:53): 1 packets",
		"ICMP to 10.9.9.5: 1 packets",
		"TCP to 10.9.9.3:443: 1 packets",
		"UDP to 127.0.0.1:9 (a service on this computer other than your search server): 1 packets",
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("leaks:\n%q\nwant\n%q", list, want)
	}
}

func TestClassifyRemote(t *testing.T) {
	flows := []flow{
		{proto: "tcp", src: "192.168.4.20", dst: "192.168.4.50", sport: 8732, dport: 50000, packets: 5}, // phone on home Wi-Fi
		{proto: "tcp", src: "100.121.116.8", dst: "100.88.1.2", sport: 8732, dport: 50001, packets: 4},  // phone over Tailscale
		{proto: "tcp", src: "192.168.4.20", dst: "8.8.8.8", sport: 8732, dport: 50002, packets: 1},      // not a phone
	}
	list, _, _ := classify(flows, policy{local: map[string]bool{}, endpoint: "1.2.3.4:51820", remotePort: "8732", remoteTailscale: true})
	want := []string{"TCP to 8.8.8.8:50002: 1 packets"}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("leaks %q, want %q", list, want)
	}
}

// Direct traffic is allowed to the user's search server (local or on the
// home network) and from the app's search proxy, nothing else local.
func TestClassifyLocalServices(t *testing.T) {
	p := policy{
		local: map[string]bool{"192.168.4.20": true}, endpoint: "1.2.3.4:51820",
		servers: map[string]bool{"127.0.0.1:9696": true, "192.168.4.26:9117": true},
		listen:  map[int]bool{40123: true},
	}
	flows := []flow{
		{proto: "tcp", src: "127.0.0.1", dst: "127.0.0.1", sport: 50000, dport: 9696, packets: 3},       // Prowlarr here
		{proto: "tcp", src: "192.168.4.20", dst: "192.168.4.26", sport: 50001, dport: 9117, packets: 2}, // Jackett on the NAS
		{proto: "tcp", src: "127.0.0.1", dst: "127.0.0.1", sport: 40123, dport: 50002, packets: 4},      // proxy replying
		{proto: "tcp", src: "127.0.0.1", dst: "127.0.0.1", sport: 50003, dport: 8118, packets: 1},       // a local HTTP proxy
	}
	list, _, _ := classify(flows, p)
	want := []string{"TCP to 127.0.0.1:8118 (a service on this computer other than your search server): 1 packets"}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("leaks %q, want %q", list, want)
	}
}
