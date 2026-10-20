//go:build linux

package main

// Linux: the watched processes run in their own cgroup (a systemd scope),
// and an nftables table counts every packet any process in that cgroup
// sends, keyed by addresses, protocol and ports. The kernel matches packets
// to the cgroup through their socket, so every TCP and UDP packet counts,
// however short-lived the connection, including UDP sent without connect()
// and traffic from every process the app starts (WebKit's helpers).
//
// System DNS: glibc normally asks systemd-resolved over a Unix socket
// (nss-resolve), which sends no packet from the app. The watched processes
// therefore get a private /etc/nsswitch.conf ("files myhostname dns"), so
// any system lookup becomes a DNS packet, to the 127.0.0.53 stub or a DNS
// server, and is counted like everything else. The answers are the same.
// /run/nscd is hidden from them too, so glibc can't hand lookups to nscd.
//
// Not seen, by design: work other processes do on the app's behalf, outside
// its cgroup (D-Bus calls to systemd-resolved, desktop portals, avahi). And
// UDP to the WireGuard endpoint is allowed from any watched process, not
// only the main one as on Windows.
//
// It runs as the logged-in user and uses sudo (asked once) for nft and for
// the private mount; the watched processes run as the user, with the user's
// environment, so the app finds its keyring and the desktop as usual.
//
// Allowed: traffic to this computer (except DNS port 53), UDP to the VPN
// server's WireGuard endpoint, and the remote control's replies to the home
// network or Tailscale. Everything else is reported.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sharkbite/internal/systool"
	"sharkbite/internal/warp"
)

const (
	nftTable     = "sbt_leakcheck"
	linuxAppComm = "SharkBiteTorren" // the kernel's 15-character process name
	linuxExe     = "/usr/bin/sharkbitetorrent"
)

// flow is one counted set element: packets the watched cgroup sent.
type flow struct {
	proto              string // tcp, udp or an IP protocol name
	src, dst           string // IP addresses
	sport, dport       int
	packets, bytesSent int64
}

func (f flow) to() string { return net.JoinHostPort(f.dst, strconv.Itoa(f.dport)) }

func runLinux(secs int, selftest, app bool, exe, endpointFlag, childWarp string) int {
	if childWarp != "" {
		return runChild(childWarp, secs, selftest)
	}
	if exe == defaultExe() {
		exe = linuxExe
	}
	if secs == 0 {
		secs = 60
		if app {
			secs = 150
		} else if selftest {
			secs = 10
		}
	}

	var endpoint, remotePort string
	var remoteTailscale bool
	var childArgs []string
	if app {
		if appRunning() {
			return fatalCode("quit Shark BiteTorrent first (tray icon → Quit): this check launches it itself, to cover its startup")
		}
		if _, err := os.Stat(exe); err != nil {
			return fatalCode("app not found at " + exe + " (use -exe)")
		}
		endpoint = endpointFlag
		remotePort, remoteTailscale = appRemotePort()
		if endpoint == "" {
			var err error
			if endpoint, err = appEndpoint(); err != nil {
				return fatalCode(err.Error())
			}
		}
		childArgs = []string{exe}
	} else {
		if len(flag.Args()) != 1 {
			return fatalCode("usage: leakcheck [-selftest] [-seconds N] <magnet|file.torrent>   or   leakcheck -app")
		}
		// A throwaway WARP account, registered here, outside the watched
		// cgroup: registering is a direct API call by design.
		tmp, err := os.MkdirTemp("", "leakcheck")
		if err != nil {
			return fatalCode(err.Error())
		}
		defer os.RemoveAll(tmp)
		// Kept as plain JSON in the private temp folder, not through the
		// app's secret storage, which could create its key file.
		acc, err := warp.Register()
		if err != nil {
			return fatalCode("registering a test WARP account: " + err.Error())
		}
		warpFile := filepath.Join(tmp, "warp.json")
		data, _ := json.Marshal(acc)
		if err := os.WriteFile(warpFile, data, 0o600); err != nil {
			return fatalCode(err.Error())
		}
		endpoint = acc.Endpoint
		self, err := os.Executable()
		if err != nil {
			return fatalCode(err.Error())
		}
		childArgs = []string{self, "-child", warpFile, "-seconds", strconv.Itoa(secs)}
		if selftest {
			childArgs = append(childArgs, "-selftest")
		}
		childArgs = append(childArgs, flag.Args()[0])
	}
	endpoint = normalizeHostPort(endpoint)

	fmt.Println("YOU: if sudo asks for your password, type it (it's needed for the firewall counters and the private DNS setup).")
	if err := sudoInteractive("-v"); err != nil {
		return fatalCode("sudo: " + err.Error())
	}
	sudo("nft", "delete", "table", "inet", nftTable) // left over from an interrupted run
	// Keep sudo's timestamp fresh, so reading the counters and removing the
	// table at the end still work after a long run.
	keepAlive := time.NewTicker(60 * time.Second)
	defer keepAlive.Stop()
	go func() {
		for range keepAlive.C {
			sudo("-v")
		}
	}()

	nss, err := os.CreateTemp("", "leakcheck-nsswitch")
	if err != nil {
		return fatalCode(err.Error())
	}
	defer os.Remove(nss.Name())
	nss.WriteString("hosts: files myhostname dns\n")
	nss.Close()
	os.Chmod(nss.Name(), 0o644)

	// The watched command: in a new systemd scope (its own cgroup), as root
	// only long enough to bind-mount the private nsswitch.conf in a private
	// mount namespace, then waiting for "go" (sent once the counters are in
	// place) and dropping back to this user.
	unit := fmt.Sprintf("sbt-leakcheck-%d", os.Getpid())
	home, _ := os.UserHomeDir()
	// $1 nsswitch file, $2 uid, $3 gid, $4 home, $5 USER=…, then the command;
	// env puts HOME and USER back after sudo.
	script := `mount --bind "$1" /etc/nsswitch.conf || exit 1
for d in /run/nscd /var/run/nscd; do if [ -d "$d" ] && ! mountpoint -q "$d"; then mount -t tmpfs tmpfs "$d" || exit 1; fi; done
read go || exit 1; exec setpriv --reuid="$2" --regid="$3" --init-groups --inh-caps=-all -- env HOME="$4" "$5" "${@:6}"`
	args := []string{"--user", "--scope", "--quiet", "--unit=" + unit, "--",
		mustFind("sudo"), "-n", "-E", "--", mustFind("unshare"), "--mount", "--propagation", "private", "--",
		mustFind("bash"), "-c", script, "leakcheck", nss.Name(),
		strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), home, "USER=" + os.Getenv("USER")}
	args = append(args, childArgs...)
	cmd := systool.Command("systemd-run", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fatalCode(err.Error())
	}
	var appLog bytes.Buffer
	if app {
		cmd.Stdout, cmd.Stderr = &appLog, &appLog
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Ctrl+C reaches only us; we stop the scope
	if err := cmd.Start(); err != nil {
		return fatalCode("starting the watched process: " + err.Error())
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	cg, err := scopeCgroup(cmd.Process.Pid, unit)
	if err != nil {
		sudo("kill", "-KILL", strconv.Itoa(cmd.Process.Pid))
		return fatalCode(err.Error())
	}
	// Stops everything in the scope, root-owned parts included (sudo may put
	// the command in a session of its own, so a process-group kill can miss).
	stop := func() {
		sudo("sh", "-c", "echo 1 > /sys/fs/cgroup/"+cg+"/cgroup.kill")
	}
	if out, err := sudoInput(nftRules(cg), "nft", "-f", "-"); err != nil {
		stop()
		return fatalCode("installing the nftables counters: " + err.Error() + ": " + strings.TrimSpace(string(out)))
	}
	cleanup := func() {
		if out, err := sudo("nft", "delete", "table", "inet", nftTable); err != nil && !bytes.Contains(out, []byte("No such file")) {
			fmt.Fprintf(os.Stderr, "leakcheck: couldn't remove the counters (%v); remove them with: sudo nft delete table inet %s\n", err, nftTable)
		}
	}
	defer cleanup()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		fmt.Println("\ninterrupted")
		stop()
		cleanup()
		os.Exit(130)
	}()

	fmt.Fprintln(stdin, "go")
	stdin.Close()
	start := time.Now()

	if app {
		fmt.Printf("Started Shark BiteTorrent; watching it for %d seconds.\nYOU: leave the window alone for the first minute (that covers startup), then type in the search box and search, type or paste in the magnet box, open every Settings section (type in a text field, click through VPN providers without saving), and click Check for updates.\n", secs)
		select {
		case err := <-exited:
			return fatalCode(fmt.Sprintf("the app exited after %ds (%v): %s", int(time.Since(start).Seconds()), err, lastLines(appLog.String(), 10)))
		case <-time.After(time.Duration(secs) * time.Second):
		}
		// Quitting is covered too. (The app ignores SIGTERM for now, so the
		// person running the check quits it.)
		fmt.Println("\nYOU: now quit Shark BiteTorrent from its tray icon (right-click → Quit). The copy this check started used a private DNS setup; start it again afterwards (the usual way).")
		select {
		case <-exited:
		case <-time.After(2 * time.Minute):
			fmt.Println("The app is still running; stopping it.")
			stop()
			<-exited
		}
	} else {
		select {
		case err := <-exited:
			if err != nil {
				return fatalCode("the engine run failed: " + err.Error())
			}
		case <-time.After(time.Duration(secs)*time.Second + 3*time.Minute):
			stop()
			return fatalCode("the engine run didn't finish")
		}
	}

	out, err := sudo("nft", "-j", "list", "table", "inet", nftTable)
	if err != nil {
		return fatalCode("reading the counters: " + err.Error())
	}
	flows, total, err := parseNft(out)
	if err != nil {
		return fatalCode("reading the counters: " + err.Error())
	}
	return verdict(flows, total, endpoint, remotePort, remoteTailscale, selftest)
}

// runChild is the engine run inside the watched cgroup.
func runChild(warpFile string, secs int, selftest bool) int {
	data, err := os.ReadFile(warpFile)
	if err != nil {
		return fatalCode(err.Error())
	}
	var acc *warp.Account
	if err := json.Unmarshal(data, &acc); err != nil || acc == nil {
		return fatalCode("reading the test WARP account: " + errText(err))
	}
	if err := runEngine(acc, secs, selftest, flag.Args()[0]); err != nil {
		return fatalCode(err.Error())
	}
	return 0
}

func verdict(flows []flow, total int64, endpoint, remotePort string, remoteTailscale, selftest bool) int {
	list, counted, tunnel := classify(flows, localAddrs(), endpoint, remotePort, remoteTailscale)
	fmt.Printf("\npackets from the watched processes: %d (tunnel %d)\n", total, tunnel)
	if counted != total {
		// The per-address sets missed packets (full?), so the details are
		// incomplete; the total counter can't miss.
		fmt.Printf("FAIL: %d packets weren't attributed to an address, so they couldn't be checked\n", total-counted)
		return 1
	}
	if tunnel == 0 {
		fmt.Println("FAIL: the VPN tunnel's own traffic (UDP to " + endpoint + ") wasn't counted, so nothing was verified")
		return 1
	}
	if selftest {
		missed := []string{}
		if !containsAny(list, plantedConn) {
			missed = append(missed, "the direct connection to "+plantedConn)
		}
		if !containsAny(list, "DNS lookup") {
			missed = append(missed, "the system DNS lookup of "+plantedLookup)
		}
		if len(missed) > 0 {
			fmt.Println("selftest FAILED: the check missed", strings.Join(missed, " and "))
			return 1
		}
		fmt.Println("selftest OK: caught both planted leaks:")
		for _, l := range list {
			fmt.Println("  ", l)
		}
		return 0
	}
	if len(list) > 0 {
		fmt.Println("FAIL: traffic outside the tunnel:")
		for _, l := range list {
			fmt.Println("  ", l)
		}
		return 1
	}
	fmt.Println("PASS: only the VPN tunnel and this computer were contacted; no system DNS lookups")
	return 0
}

// classify sorts the counted flows into the tunnel, allowed traffic and
// leaks (returned as text with packet counts).
func classify(flows []flow, local map[string]bool, endpoint, remotePort string, remoteTailscale bool) (list []string, counted, tunnel int64) {
	type leak struct {
		text    string
		packets int64
	}
	leaks := map[string]*leak{}
	add := func(text string, n int64) {
		if leaks[text] == nil {
			leaks[text] = &leak{text: text}
		}
		leaks[text].packets += n
	}
	for _, f := range flows {
		counted += f.packets
		switch {
		case f.dport == 53 && (f.proto == "udp" || f.proto == "tcp"):
			add("DNS lookup outside the tunnel (to "+f.to()+")", f.packets)
		case local[f.dst] || net.ParseIP(f.dst).IsLoopback():
		case f.proto == "udp" && normalizeHostPort(f.to()) == endpoint:
			tunnel += f.packets
		case remotePort != "" && f.proto == "tcp" && strconv.Itoa(f.sport) == remotePort &&
			(privateAddr(f.to()) && !tailscaleAddr(net.JoinHostPort(f.src, "0")) ||
				remoteTailscale && tailscaleAddr(f.to()) && tailscaleAddr(net.JoinHostPort(f.src, "0"))):
			// The remote control (on in Settings) answering a phone.
		default:
			text := strings.ToUpper(f.proto) + " to " + f.dst
			if f.proto == "tcp" || f.proto == "udp" {
				text = strings.ToUpper(f.proto) + " to " + f.to()
			}
			add(text, f.packets)
		}
	}
	for _, l := range leaks {
		list = append(list, fmt.Sprintf("%s: %d packets", l.text, l.packets))
	}
	sort.Strings(list)
	return list, counted, tunnel
}

// nftRules counts every packet sent by processes in cgroup (a path relative
// to /sys/fs/cgroup), per source, destination and ports.
func nftRules(cgroup string) string {
	level := strings.Count(cgroup, "/") + 1
	set := func(name, addr, rest string) string {
		return fmt.Sprintf("\tset %s { type %s . %s%s; flags dynamic; counter; size 65535; }\n", name, addr, addr, rest)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", nftTable)
	b.WriteString("\tcounter total {}\n")
	b.WriteString(set("tcp4", "ipv4_addr", " . inet_service . inet_service"))
	b.WriteString(set("udp4", "ipv4_addr", " . inet_service . inet_service"))
	b.WriteString(set("tcp6", "ipv6_addr", " . inet_service . inet_service"))
	b.WriteString(set("udp6", "ipv6_addr", " . inet_service . inet_service"))
	b.WriteString(set("other4", "ipv4_addr", " . inet_proto"))
	b.WriteString(set("other6", "ipv6_addr", " . inet_proto"))
	fmt.Fprintf(&b, "\tchain out {\n\t\ttype filter hook output priority -400; policy accept;\n\t\tsocket cgroupv2 level %d %q goto watched\n\t}\n", level, cgroup)
	b.WriteString(`	chain watched {
		counter name "total"
		meta nfproto ipv4 meta l4proto tcp update @tcp4 { ip saddr . ip daddr . tcp sport . tcp dport } return
		meta nfproto ipv4 meta l4proto udp update @udp4 { ip saddr . ip daddr . udp sport . udp dport } return
		meta nfproto ipv6 meta l4proto tcp update @tcp6 { ip6 saddr . ip6 daddr . tcp sport . tcp dport } return
		meta nfproto ipv6 meta l4proto udp update @udp6 { ip6 saddr . ip6 daddr . udp sport . udp dport } return
		meta nfproto ipv4 update @other4 { ip saddr . ip daddr . meta l4proto } return
		meta nfproto ipv6 update @other6 { ip6 saddr . ip6 daddr . meta l4proto } return
	}
}
`)
	return b.String()
}

// parseNft reads the sets' elements and the total counter from
// `nft -j list table`.
func parseNft(data []byte) ([]flow, int64, error) {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, err
	}
	var flows []flow
	var total int64 = -1
	for _, obj := range doc.Nftables {
		if raw, ok := obj["counter"]; ok {
			var c struct {
				Name    string `json:"name"`
				Packets int64  `json:"packets"`
			}
			json.Unmarshal(raw, &c)
			if c.Name == "total" {
				total = c.Packets
			}
			continue
		}
		raw, ok := obj["set"]
		if !ok {
			continue
		}
		var s struct {
			Name string `json:"name"`
			Elem []struct {
				Elem struct {
					Val struct {
						Concat []json.RawMessage `json:"concat"`
					} `json:"val"`
					Counter struct {
						Packets int64 `json:"packets"`
						Bytes   int64 `json:"bytes"`
					} `json:"counter"`
				} `json:"elem"`
			} `json:"elem"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, 0, fmt.Errorf("set: %w", err)
		}
		proto := strings.TrimRight(s.Name, "46")
		for _, e := range s.Elem {
			v := e.Elem.Val.Concat
			f := flow{proto: proto, packets: e.Elem.Counter.Packets, bytesSent: e.Elem.Counter.Bytes}
			if len(v) < 3 {
				return nil, 0, fmt.Errorf("set %s: unexpected element", s.Name)
			}
			json.Unmarshal(v[0], &f.src)
			json.Unmarshal(v[1], &f.dst)
			if proto == "other" {
				json.Unmarshal(v[2], &f.proto) // "icmp", "ipv6-icmp", …
				if f.proto == "" {
					var n int
					json.Unmarshal(v[2], &n)
					f.proto = "protocol " + strconv.Itoa(n)
				}
			} else if len(v) == 4 {
				json.Unmarshal(v[2], &f.sport)
				json.Unmarshal(v[3], &f.dport)
			}
			flows = append(flows, f)
		}
	}
	if total < 0 {
		return nil, 0, errors.New("no total counter")
	}
	return flows, total, nil
}

// scopeCgroup waits until systemd has moved pid into the new scope and
// returns its cgroup path relative to /sys/fs/cgroup.
func scopeCgroup(pid int, unit string) (string, error) {
	for i := 0; i < 100; i++ {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
		if err != nil {
			return "", fmt.Errorf("the watched process exited at once: %w", err)
		}
		p := strings.TrimSpace(strings.TrimPrefix(string(b), "0::"))
		if strings.HasSuffix(p, "/"+unit+".scope") {
			return strings.TrimPrefix(p, "/"), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", errors.New("systemd didn't create the scope for the watched process")
}

// localAddrs are this computer's own addresses (loopback included).
func localAddrs() map[string]bool {
	m := map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			m[ipn.IP.String()] = true
		}
	}
	for _, s := range []string{"127.0.0.1", "::1"} {
		m[s] = true
	}
	return m
}

func appRunning() bool {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		// The package's /usr/bin/sharkbitetorrent shows as "sharkbitetorren".
		if b, err := os.ReadFile("/proc/" + e.Name() + "/comm"); err == nil && strings.EqualFold(strings.TrimSpace(string(b)), linuxAppComm) {
			return true
		}
	}
	return false
}

func mustFind(name string) string {
	if p, ok := systool.Find(name); ok {
		return p
	}
	return name
}

func sudo(args ...string) ([]byte, error) {
	return systool.Command("sudo", append([]string{"-n"}, args...)...).CombinedOutput()
}

func sudoInput(input string, args ...string) ([]byte, error) {
	cmd := systool.Command("sudo", append([]string{"-n"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.CombinedOutput()
}

func sudoInteractive(args ...string) error {
	cmd := systool.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
