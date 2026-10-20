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
	list, counted, tunnel := classify(flows, map[string]bool{"10.9.9.1": true}, "10.9.9.2:51820", "", false)
	if counted != total || tunnel != 3 {
		t.Errorf("counted %d of %d packets, tunnel %d", counted, total, tunnel)
	}
	want := []string{
		"DNS lookup outside the tunnel (to 127.0.0.53:53): 1 packets",
		"ICMP to 10.9.9.5: 1 packets",
		"TCP to 10.9.9.3:443: 1 packets",
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
	list, _, _ := classify(flows, map[string]bool{}, "1.2.3.4:51820", "8732", true)
	want := []string{"TCP to 8.8.8.8:50002: 1 packets"}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("leaks %q, want %q", list, want)
	}
}
