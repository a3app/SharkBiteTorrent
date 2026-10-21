//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"
)

// Lines as tcpdump -i pktap,all -k NP -nn prints them (from real captures).
const sampleCapture = `00:59:09.510576 (proc SharkBiteTorrent:16734) IP 192.168.4.51.59785 > 212.56.53.55.1337: UDP, length 96
00:59:09.510624 (proc SharkBiteTorrent:16734) IP 192.168.4.51.59785 > 212.56.53.55.1337: UDP, length 96
00:59:09.600000 (proc SharkBiteTorrent:16734) IP 212.56.53.55.1337 > 192.168.4.51.59785: UDP, length 1400
00:59:12.522988 (proc mDNSResponder:656, eproc dscacheutil:17455) IP 100.72.141.16.53153 > 100.100.100.100.53: 48106+ AAAA? leakprobe-marker.example.com. (46)
00:59:35.048311 (proc mDNSResponder:656, eproc SharkBiteTorrent:16734) IP 100.72.141.16.63464 > 100.100.100.100.53: 31886+ A? www.privateinternetaccess.com. (44)
00:59:35.082717 () IP 100.100.100.100.53 > 100.72.141.16.63464: 31886 2/0/0 A 162.159.61.4, A 172.64.41.4 (76)
01:47:07.656844 () IP 100.72.141.16.49543 > 100.100.100.100.53: 29765+ Type65? gdmf.apple.com. (32)
00:59:36.000000 (proc SharkBiteTorrent:16734) IP 192.168.4.51.50000 > 162.159.61.4.443: Flags [S], seq 1, win 65535, length 0
00:59:36.100000 (proc com.apple.WebKit.Networking:16740) IP6 2600:1702::69.51147 > 2a04:4e42:4c::347.443: Flags [P.], seq 1:100, ack 1, win 2048, length 99
00:59:37.000000 (proc kernel_task:0) IP 192.168.4.51.49152 > 192.168.4.26.445: Flags [.], ack 1, win 2048, length 0
00:59:38.000000 (proc Discord Helper:999) IP 192.168.4.51.50001 > 162.159.136.234.443: Flags [S], seq 1, win 65535, length 0
00:59:39.000000 (proc SharkBiteTorrent:16734) IP 192.168.4.51 > 8.8.8.8: ICMP echo request, id 1, seq 1, length 64
00:59:29.885458 (proc mDNSResponder:656, eproc :656) IP 192.168.4.1.5353 > 224.0.0.251.5353: 0 [6q] SRV (QM)? x._trel._udp.local. (60)
00:59:41.000000 (proc leakcheck:16734, in) IP6 ::1.9696 > ::1.50736: Flags [P.], seq 1:100, ack 1, win 6000, length 99
00:59:41.000001 (proc leakcheck:16734, out) IP6 ::1.50736 > ::1.9696: Flags [.], ack 100, win 6000, length 0
02:00:00.000001 (out) IP 192.168.4.51.50790 > 1.1.1.1.443: Flags [S], seq 1, win 65535, length 0
02:00:00.000002 (proc :0, eproc leakcheck:16734, in) IP 1.1.1.1.443 > 192.168.4.51.50790: Flags [S.], seq 1, ack 2, win 65535, length 0
02:00:00.000003 (out) IP 192.168.4.51.50791 > 9.9.9.9.53: 1+ A? x. (20)
02:00:00.000006 (out) IP 192.168.4.51.50792 > 203.0.113.9.6881: UDP, length 20
02:00:00.000007 (out) IP 192.168.4.51 > 162.159.192.8: ICMP 192.168.4.51 udp port 2408 unreachable, length 36
02:00:00.000004 (proc Prowlarr:700, out) IP 127.0.0.1.60000 > 127.0.0.1.49602: Flags [P.], seq 1:10, ack 1, win 6000, length 9
02:00:00.000005 (proc SharkBiteTorrent:16734, in) IP 127.0.0.1.60000 > 127.0.0.1.49602: Flags [P.], seq 1:10, ack 1, win 6000, length 9
01:58:18.822040 (in) IP 192.168.4.26.45148 > 192.168.7.255.32412: UDP, length 21
00:59:40.000000 ARP, Request who-has 192.168.4.1 tell 192.168.4.51, length 28
`

func parseSample(t *testing.T) *pktapResult {
	t.Helper()
	r := newPktapResult()
	for _, l := range strings.Split(sampleCapture, "\n") {
		r.add(l)
	}
	return r
}

func TestPktapParse(t *testing.T) {
	r := parseSample(t)
	k := pktapKey{proc: 16734, proto: "udp", src: "192.168.4.51", dst: "212.56.53.55", sport: 59785, dport: 1337}
	if r.counts[k] != 2 {
		t.Errorf("tunnel packets: %d, want 2 (%v)", r.counts[k], r.counts)
	}
	if r.queryNames["100.100.100.100|31886"] != "www.privateinternetaccess.com" || r.queryNames["100.100.100.100|29765"] != "gdmf.apple.com" {
		t.Errorf("queries (Type65 included): %v", r.queryNames)
	}
	if got := r.answersByName()["www.privateinternetaccess.com"]; len(got) != 2 || got[0] != "162.159.61.4" || got[1] != "172.64.41.4" {
		t.Errorf("answers: %v", r.answersByName())
	}
	v6 := pktapKey{proc: 16740, proto: "tcp", src: "2600:1702::69", dst: "2a04:4e42:4c::347", sport: 51147, dport: 443}
	if r.counts[v6] != 1 {
		t.Errorf("IPv6 TCP not parsed: %v", r.counts)
	}
	if r.counts[pktapKey{proc: 16734, proto: "icmp", src: "192.168.4.51", dst: "8.8.8.8"}] != 1 {
		t.Errorf("ICMP not parsed: %v", r.counts)
	}
	if r.counts[pktapKey{proc: 999, proto: "tcp", src: "192.168.4.51", dst: "162.159.136.234", sport: 50001, dport: 443}] != 1 {
		t.Error("a process name with a space wasn't parsed")
	}
}

func TestSplitPktapAddr(t *testing.T) {
	cases := []struct {
		in   string
		v6   bool
		addr string
		port int
		ok   bool
	}{
		{"192.168.4.51.59785", false, "192.168.4.51", 59785, true},
		{"8.8.8.8:", false, "8.8.8.8", 0, false},
		{"2600:1702::69.443", true, "2600:1702::69", 443, true},
		{"fe80::1%en0.5353", true, "fe80::1", 5353, true},
		{"ff02::fb", true, "ff02::fb", 0, false},
	}
	for _, c := range cases {
		a, p, ok := splitPktapAddr(c.in, c.v6)
		if a != c.addr || p != c.port || ok != c.ok {
			t.Errorf("%s: got %s %d %v", c.in, a, p, ok)
		}
	}
}

func TestParseMDNSLine(t *testing.T) {
	r, ok := parseMDNSLine(`2026-10-21 00:59:24.799 Df mDNSResponder[656:145d] [com.apple.mDNSResponder:Default] [R6739] DNSServiceQueryRecord START -- qname: <private> <dn:72K6SQ>, qtype: A, flags: 0x15000, interface index: 0, client pid: 16734 (SharkBiteTorren), name hash: <dn:72K6SQ>`)
	want := time.Date(2026, 10, 21, 0, 59, 24, 799e6, time.Local)
	if !ok || r.mdnsPID != 656 || r.client != 16734 || r.hash != "72K6SQ" || !r.at.Equal(want) {
		t.Errorf("parsed %+v %v", r, ok)
	}
	// With private data shown, the name itself.
	r, ok = parseMDNSLine(`2026-10-21 00:59:24.799 Df mDNSResponder[656:145d] [com.apple.mDNSResponder:Default] [R1] DNSServiceGetAddrInfo START -- hostname: Example.COM., flags: 0x0, client pid: 5 (x)`)
	if !ok || r.hash != "example.com" || r.client != 5 {
		t.Errorf("parsed %+v %v", r, ok)
	}
	if _, ok := parseMDNSLine("Filtering the log data using \"process == …\""); ok {
		t.Error("parsed a non-request line")
	}
}

// The app's own packets and its WebKit helper's are judged; mDNSResponder's
// (lookups are judged from its log), other programs' traffic and what
// arrives aren't. Lookups are named by the hashes learned from this
// process's own lookups, in their time windows.
func TestDarwinVerdictRules(t *testing.T) {
	r := parseSample(t)
	w := newMacWatch(16734)
	w.pids[16740] = true // a WebKit helper launchd says the app is responsible for
	local := map[string]bool{"192.168.4.51": true, "100.72.141.16": true, "2600:1702::69": true, "127.0.0.1": true, "::1": true}
	flows, un := r.forWatched(w, local, map[int]bool{656: true})
	if r.withDir != 10 {
		t.Errorf("directions parsed: %d", r.withDir)
	}

	own := 500
	t0 := time.Date(2026, 10, 21, 1, 0, 0, 0, time.Local)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	learned := []timedLookup{
		{name: "localhost", from: at(0), to: at(5)}, // answered without mDNSResponder: no request
		{name: "example.com", from: at(200), to: at(260)},
		{name: "www.privateinternetaccess.com", from: at(500), to: at(540)},
	}
	reqs := []mdnsRequest{
		{at: at(210), client: own, hash: "EXMPL"},
		{at: at(211), client: own, hash: "EXMPL"},
		{at: at(505), client: own, hash: "PIAHSH"},
		{at: at(900), client: 16734, hash: "PIAHSH"},    // the app signing in to PIA: documented
		{at: at(901), client: 16734, hash: "EXMPL"},     // a planted lookup
		{at: at(902), client: 16740, hash: "UNKNWN"},    // its WebKit helper, a name nobody taught us
		{at: at(903), client: 17455, hash: "OTHER1"},    // someone else
		{at: at(904), client: 16734, hash: "localhost"}, // shown in clear: answered here, not counted
	}
	// The planted-style connection: outgoing SYN untagged, the reply tagged
	// with the app; it counts. Untagged traffic nobody claims is reported
	// separately, not judged; Prowlarr's own sends to the proxy don't count.
	// (gdmf.apple.com's query, from a capture without directions, is
	// untagged and unclaimed too; the kernel's SMB packets are tagged.)
	if un.rescued != 1 || un.dns != 2 || un.icmp != 1 || un.unclaimed != 1 || len(un.flows) != 1 || !strings.Contains(un.flows[0], "203.0.113.9:6881") {
		t.Errorf("untagged: %+v", un)
	}
	lookups := watchedLookups(reqs, w, own, learned, r.answersByName())
	if got := lookups["www.privateinternetaccess.com"]; len(got) != 2 {
		t.Errorf("the documented lookup wasn't named with its answers: %v", lookups)
	}
	p := policy{local: local, endpoints: map[string]bool{"212.56.53.55:1337": true}, lookups: lookups,
		servers: map[string]bool{"[::1]:9696": true}} // the user's Prowlarr
	list, allowed, _, tunnel := classify(flows, p)
	if tunnel != 2 {
		t.Errorf("tunnel packets: %d (only what the app sent counts)", tunnel)
	}
	text := strings.Join(list, "\n")
	for _, want := range []string{
		"DNS lookup outside the tunnel: example.com",
		"DNS lookup outside the tunnel: a name the check doesn't know (hash dn:UNKNWN)",
		"TCP to [2a04:4e42:4c::347]:443", "ICMP to 8.8.8.8", "TCP to 1.1.1.1:443: 1 packets",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("not reported: %q\n%s", want, text)
		}
	}
	for _, not := range []string{"192.168.4.26", "162.159.136.234", "224.0.0.251", "100.100.100.100", "OTHER1", "localhost", "privateinternetaccess", "162.159.61.4", "50736", "49602", "9.9.9.9"} {
		if strings.Contains(text, not) {
			t.Errorf("reported %s, which should be allowed or isn't the app's:\n%s", not, text)
		}
	}
	all := strings.Join(allowed, "\n")
	if !strings.Contains(all, "lookup of www.privateinternetaccess.com (PIA sign-in)") || !strings.Contains(all, "HTTPS to 162.159.61.4:443 (PIA sign-in)") {
		t.Errorf("the documented sign-in wasn't allowed:\n%s", all)
	}
}

func TestControlsMissing(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 1, 0, 0, 0, time.Local)
	r := newPktapResult()
	r.counts[pktapKey{proc: 42, proto: "udp", src: "127.0.0.1", dst: "127.0.0.1", sport: 50000, dport: 9}] = 1
	reqs := []mdnsRequest{{at: t0.Add(10 * time.Millisecond), client: 42, hash: "CTRL1"}}
	c := &controls{
		packets: []string{"127.0.0.1:9 from 127.0.0.1:50000"},
		lookups: []timedLookup{{name: "leakcheck-a.example.com", from: t0, to: t0.Add(20 * time.Millisecond)},
			{name: "leakcheck-b.example.com", from: t0.Add(time.Minute), to: t0.Add(time.Minute + 20*time.Millisecond)}},
	}
	m := c.missing(r, reqs, 42)
	if len(m) != 1 || !strings.Contains(m[0], "leakcheck-b.example.com") {
		t.Errorf("missing: %v", m)
	}
	if m := c.missing(r, reqs, 43); len(m) != 3 {
		t.Errorf("another process's traffic counted as the controls: %v", m)
	}
}
