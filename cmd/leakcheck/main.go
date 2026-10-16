// Command leakcheck exercises the app's networking in VPN mode and fails if
// anything reaches the internet outside the tunnel. It's part of the release
// checklist (RELEASING.md).
//
//	go run ./cmd/leakcheck [-seconds 60] <magnet|file.torrent>
//
// While it runs it brings up WARP, starts the torrent engine and downloads
// the given torrent, loads every VPN provider's server list (as Settings
// does), and searches the user's own Prowlarr/Jackett if one is set up.
// Meanwhile it lists this process's own sockets every 250 ms (netstat). The
// only sockets allowed are the WireGuard UDP socket and connections to this
// computer (Prowlarr/Jackett on localhost). Windows only for now: it parses
// Windows netstat output.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sharkbite/internal/engine"
	"sharkbite/internal/search"
	"sharkbite/internal/secret"
	"sharkbite/internal/tunnel"
	"sharkbite/internal/vpnprov"
	"sharkbite/internal/warp"
)

func main() {
	secs := flag.Int("seconds", 60, "how long to download for")
	selftest := flag.Bool("selftest", false, "also make one deliberate direct connection, to prove the check catches it (must FAIL)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: leakcheck [-seconds N] <magnet|file.torrent>")
		os.Exit(2)
	}

	cfgDir, _ := os.UserConfigDir()
	appDir := filepath.Join(cfgDir, "SharkBiteTorrent")
	acc, err := warp.LoadOrRegister(filepath.Join(appDir, "warp.json"))
	must(err, "WARP")
	tun, err := tunnel.Start(tunnel.Config{
		PrivateKey: acc.PrivateKey, PeerPublicKey: acc.PeerPublicKey, Endpoint: acc.Endpoint,
		Addresses: []string{acc.AddressV4, acc.AddressV6}, DNS: []string{"1.1.1.1", "1.0.0.1"},
	})
	must(err, "tunnel")
	defer tun.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tr, err := tun.CheckTrace(ctx)
	cancel()
	must(err, "trace")
	fmt.Printf("tunnel up, exit %s warp=%s\n", tr.IP, tr.Warp)
	vpnprov.SetTunnel(tun.DialContext) // as the app does once connected

	// Watch from here on: setting up the tunnel itself is allowed to be
	// direct (that's the WireGuard socket).
	mon := startMonitor()

	out, _ := os.MkdirTemp("", "leakcheck")
	defer os.RemoveAll(out)
	eng, err := engine.New(tun, engine.Options{})
	must(err, "engine")
	hash, err := eng.Add(engine.AddOptions{Source: flag.Arg(0), Dir: out})
	must(err, "add")

	for _, p := range vpnprov.All() {
		prov, _ := vpnprov.Get(p.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rs, err := prov.Regions(ctx)
		cancel()
		fmt.Printf("server list %-8s %3d locations %v\n", p.ID, len(rs), errText(err))
	}

	if data, err := secret.ReadFile(filepath.Join(appDir, "search.json")); err == nil {
		var sc search.Config
		if json.Unmarshal(data, &sc) == nil && sc.Kind != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			rs, err := search.Search(ctx, sc, "debian", "")
			cancel()
			fmt.Printf("search via %s: %d results %v\n", sc.Kind, len(rs), errText(err))
		}
	}

	if *selftest {
		if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 10*time.Second); err == nil {
			fmt.Println("selftest: opened a direct connection to 1.1.1.1:443")
			time.Sleep(time.Second)
			c.Close()
		}
	}

	deadline := time.Now().Add(time.Duration(*secs) * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range eng.List() {
			if s.Hash == hash {
				fmt.Printf("\r%-12s %5.1f%%  %6.0f KB/s  peers %d   ", s.State, s.Progress*100, float64(s.DownRate)/1024, s.Peers)
			}
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Println()
	eng.Remove(hash, true)
	eng.Close()

	leaks, samples := mon.stop()
	fmt.Printf("checked this process's sockets %d times\n", samples)
	if len(leaks) > 0 {
		fmt.Println("FAIL: traffic outside the tunnel:")
		for l := range leaks {
			fmt.Println("  ", l)
		}
		os.Exit(1)
	}
	fmt.Println("PASS: nothing left this computer except through the WireGuard tunnel")
}

type monitor struct {
	stopc   chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	leaks   map[string]bool
	samples int
}

func startMonitor() *monitor {
	m := &monitor{stopc: make(chan struct{}), done: make(chan struct{}), leaks: map[string]bool{}}
	pid := strconv.Itoa(os.Getpid())
	baselineUDP := udpSockets(pid) // the WireGuard socket
	go func() {
		defer close(m.done)
		for {
			select {
			case <-m.stopc:
				return
			case <-time.After(250 * time.Millisecond):
			}
			m.check(pid, baselineUDP)
		}
	}()
	return m
}

func (m *monitor) stop() (map[string]bool, int) {
	close(m.stopc)
	<-m.done
	return m.leaks, m.samples
}

func (m *monitor) check(pid string, baselineUDP map[string]bool) {
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples++
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[len(f)-1] != pid {
			continue
		}
		switch f[0] {
		case "TCP":
			// TCP local remote state pid
			if len(f) == 5 && f[3] != "LISTENING" && !isLocal(f[2]) {
				m.leaks["TCP to "+f[2]] = true
			}
		case "UDP":
			if !baselineUDP[f[1]] {
				m.leaks["UDP socket "+f[1]] = true
			}
		}
	}
}

func udpSockets(pid string) map[string]bool {
	out, _ := exec.Command("netstat", "-ano", "-p", "UDP").Output()
	v6, _ := exec.Command("netstat", "-ano", "-p", "UDPv6").Output()
	got := map[string]bool{}
	for _, line := range strings.Split(string(out)+string(v6), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "UDP" && f[len(f)-1] == pid {
			got[f[1]] = true
		}
	}
	return got
}

func isLocal(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func errText(err error) string {
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return ""
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
