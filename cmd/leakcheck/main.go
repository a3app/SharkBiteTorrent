// Command leakcheck verifies that nothing leaves this computer outside the
// VPN tunnel. It's part of the release checklist (RELEASING.md). Windows
// only, and it must run as administrator: it turns on Windows' own logging
// of network connections and DNS lookups for the duration, then puts the
// previous settings back.
//
//	go run ./cmd/leakcheck [-seconds 60] [-selftest] <magnet|file.torrent>
//	go run ./cmd/leakcheck -app [-seconds 120]
//
// The first form exercises the engine in VPN mode (WARP, on a throwaway
// account): it downloads the torrent, loads every provider's server list and
// searches the user's Prowlarr/Jackett if one is set up. -selftest also makes
// a deliberate short direct connection and a system DNS lookup, which the
// check must catch (the run must FAIL).
//
// -app watches the installed, running Shark BiteTorrent (and its WebView2
// processes) while you use it.
//
// How it sees everything:
//   - Windows Filtering Platform auditing (Security log, events 5156/5157)
//     records every connection the OS permits or blocks, TCP and UDP, however
//     short, with the process ID. Polling netstat misses short connections.
//   - The DNS Client log (event 3006) records lookups through the system
//     resolver with the calling process, which no socket listing shows.
//
// Allowed: anything to this computer (loopback), and the WireGuard socket's
// UDP traffic to the VPN server. Everything else is reported.
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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

const (
	wfpSubcategory = "Filtering Platform Connection"
	dnsLog         = "Microsoft-Windows-DNS-Client/Operational"
)

func main() { os.Exit(run()) }

// run does the work and returns the exit code, so deferred cleanup (putting
// the auditing settings back) always happens; os.Exit would skip it.
func run() int {
	secs := flag.Int("seconds", 0, "how long to run (default 60, or 120 with -app)")
	selftest := flag.Bool("selftest", false, "also make a deliberate direct connection and DNS lookup; the run must FAIL")
	app := flag.Bool("app", false, "watch the installed, running app instead of running the engine")
	flag.Parse()
	if runtime.GOOS != "windows" {
		return fatalCode("leakcheck only runs on Windows for now (it uses Windows' connection auditing)")
	}
	if !isAdmin() {
		return fatalCode("run this from an administrator terminal: it needs to turn on Windows' connection auditing")
	}
	if *secs == 0 {
		*secs = 60
		if *app {
			*secs = 120
		}
	}

	// The engine run uses a throwaway WARP account. Registering it is a direct
	// API call by design (it's how a WARP tunnel comes to exist), so it
	// happens before the auditing starts.
	var acc *warp.Account
	if !*app {
		tmp, err := os.MkdirTemp("", "leakcheck")
		if err != nil {
			return fatalCode(err.Error())
		}
		defer os.RemoveAll(tmp)
		if acc, err = warp.LoadOrRegister(filepath.Join(tmp, "warp.json")); err != nil {
			return fatalCode("registering a test WARP account: " + err.Error())
		}
	}

	restore, err := startAuditing()
	if err != nil {
		return fatalCode("turning on auditing: " + err.Error())
	}
	defer restore()
	// Give the audit policy a moment to take effect before anything runs.
	time.Sleep(time.Second)
	start := time.Now().UTC()

	var watched *pidSet
	allowed := map[string]bool{} // "udp ip:port" of the WireGuard endpoint
	// Control: a connection of our own (to this computer) that the audit log
	// must record. It proves auditing works, so silence from the app means
	// clean rather than "not logged". (A running app's VPN connection
	// started before the check, so it logs no new event.)
	control := controlConnection()
	if *app {
		watched, err = watchApp(*secs, allowed)
	} else {
		if flag.NArg() != 1 {
			return fatalCode("usage: leakcheck [-seconds N] [-selftest] <magnet|file.torrent>   or   leakcheck -app")
		}
		watched, err = runEngine(acc, *secs, *selftest, flag.Arg(0), allowed)
	}
	if err != nil {
		restore()
		return fatalCode(err.Error())
	}
	time.Sleep(2 * time.Second) // let the last events reach the logs

	conns, err := connectionEvents(start, watched)
	if err != nil {
		return fatalCode("reading the connection log: " + err.Error())
	}
	lookups, dnsErr := dnsEvents(start, watched)
	controlSeen := false
	if ownConns, err := connectionEvents(start, newPIDSet(os.Getpid())); err == nil {
		for _, c := range ownConns {
			if c.dst == control || strings.HasSuffix(control, ":"+portOf(c.dst)) {
				controlSeen = true
			}
		}
	}

	var leaks []string
	seenTunnel := false
	for _, c := range conns {
		switch {
		case c.loopback():
		case allowed[c.key()] || (c.proto == "UDP" && *app && c.outbound):
			// -app: the app's only UDP is WireGuard to its VPN server. The
			// engine run knows the exact endpoint.
			seenTunnel = true
		default:
			leaks = append(leaks, c.String())
		}
	}
	for _, q := range lookups {
		if q != "localhost" {
			leaks = append(leaks, "DNS lookup outside the tunnel: "+q)
		}
	}

	fmt.Printf("\nconnection events for the watched processes: %d; system DNS lookups: %d %v\n", len(conns), len(lookups), dedupe(lookups))
	if dnsErr != nil {
		fmt.Println("WARNING: couldn't read the DNS log:", dnsErr)
	}
	if len(leaks) > 0 {
		fmt.Println("FAIL: traffic outside the tunnel:")
		for _, l := range dedupe(leaks) {
			fmt.Println("  ", l)
		}
		return 1
	}
	if !controlSeen {
		fmt.Println("FAIL: the audit log didn't record the check's own test connection, so nothing was verified")
		return 1
	}
	if !*app && !seenTunnel {
		// The engine run starts its tunnel during the check, so its traffic
		// must show up.
		fmt.Println("FAIL: the tunnel's own traffic wasn't recorded, so nothing was verified")
		return 1
	}
	if dnsErr != nil {
		fmt.Println("FAIL: connections were clean, but DNS lookups couldn't be checked")
		return 1
	}
	fmt.Println("PASS: only the VPN tunnel and this computer were contacted; no system DNS lookups")
	return 0
}

// runEngine brings up WARP on a throwaway account and exercises the engine,
// provider APIs and search, like the app does in VPN mode.
func runEngine(acc *warp.Account, secs int, selftest bool, source string, allowed map[string]bool) (*pidSet, error) {
	allowed["udp "+acc.Endpoint] = true // the WireGuard tunnel itself
	tun, err := tunnel.Start(tunnel.Config{
		PrivateKey: acc.PrivateKey, PeerPublicKey: acc.PeerPublicKey, Endpoint: acc.Endpoint,
		Addresses: []string{acc.AddressV4, acc.AddressV6}, DNS: []string{"1.1.1.1", "1.0.0.1"},
	})
	if err != nil {
		return nil, fmt.Errorf("tunnel: %w", err)
	}
	defer tun.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tr, err := tun.CheckTrace(ctx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	fmt.Printf("tunnel up, exit %s warp=%s\n", tr.IP, tr.Warp)
	vpnprov.SetTunnel(tun.DialContext) // as the app does once connected
	defer vpnprov.SetTunnel(nil)

	out, _ := os.MkdirTemp("", "leakcheck-dl")
	defer os.RemoveAll(out)
	eng, err := engine.New(tun, engine.Options{})
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	defer eng.Close()
	hash, err := eng.Add(engine.AddOptions{Source: source, Dir: out})
	if err != nil {
		return nil, fmt.Errorf("add: %w", err)
	}

	for _, p := range vpnprov.All() {
		prov, _ := vpnprov.Get(p.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rs, err := prov.Regions(ctx)
		cancel()
		fmt.Printf("server list %-8s %3d locations %s\n", p.ID, len(rs), errText(err))
	}
	cfgDir, _ := os.UserConfigDir()
	if data, err := secret.ReadFile(filepath.Join(cfgDir, "SharkBiteTorrent", "search.json")); err == nil {
		var sc search.Config
		if json.Unmarshal(data, &sc) == nil && sc.Kind != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			rs, err := search.Search(ctx, sc, "debian", "")
			cancel()
			fmt.Printf("search via %s: %d results %s\n", sc.Kind, len(rs), errText(err))
		}
	}
	if selftest {
		// A short connection (closed at once) and a system DNS lookup: the
		// two things a socket-polling check could miss.
		if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 10*time.Second); err == nil {
			c.Close()
			fmt.Println("selftest: opened and closed a direct connection to 1.1.1.1:443")
		}
		(&net.Resolver{}).LookupHost(context.Background(), "example.com")
		fmt.Println("selftest: looked up example.com with the system resolver")
	}

	deadline := time.Now().Add(time.Duration(secs) * time.Second)
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
	return newPIDSet(os.Getpid()), nil
}

// watchApp watches the running app (and its child processes, e.g. WebView2)
// for secs seconds while the user uses it.
func watchApp(secs int, allowed map[string]bool) (*pidSet, error) {
	pid := findProcess("SharkBiteTorrent.exe")
	if pid == 0 {
		return nil, errors.New("Shark BiteTorrent isn't running")
	}
	set := newPIDSet(pid)
	fmt.Printf("Watching Shark BiteTorrent (PID %d) for %ds. Use the app now: add a torrent, search, open every Settings section, check for updates. Don't switch VPNs during the check (connecting talks to the provider directly, by design).\n", pid, secs)
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(deadline) {
		set.addChildren()
		time.Sleep(time.Second)
	}
	return set, nil
}

// controlConnection makes one loopback TCP connection and returns the
// listening address it connected to.
func controlConnection() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ""
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	if c, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		c.Close()
	}
	return ln.Addr().String()
}

func portOf(hostport string) string {
	_, p, _ := net.SplitHostPort(hostport)
	return p
}

// --- auditing on/off ---

func startAuditing() (restore func(), err error) {
	prevWFP, err := exec.Command("auditpol", "/get", "/subcategory:"+wfpSubcategory, "/r").Output()
	if err != nil {
		return nil, fmt.Errorf("auditpol: %w", err)
	}
	wasSuccess := strings.Contains(string(prevWFP), "Success")
	wasFailure := strings.Contains(string(prevWFP), "Failure")
	prevDNS, _ := exec.Command("wevtutil", "gl", dnsLog).Output()
	dnsWasEnabled := strings.Contains(string(prevDNS), "enabled: true")

	if out, err := exec.Command("auditpol", "/set", "/subcategory:"+wfpSubcategory, "/success:enable", "/failure:enable").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("auditpol: %v: %s", err, out)
	}
	if !dnsWasEnabled {
		exec.Command("wevtutil", "sl", dnsLog, "/e:true").Run()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			exec.Command("auditpol", "/set", "/subcategory:"+wfpSubcategory,
				"/success:"+onOff(wasSuccess), "/failure:"+onOff(wasFailure)).Run()
			if !dnsWasEnabled {
				exec.Command("wevtutil", "sl", dnsLog, "/e:false").Run()
			}
			fmt.Println("(auditing settings restored)")
		})
	}, nil
}

func onOff(b bool) string {
	if b {
		return "enable"
	}
	return "disable"
}

// --- reading the logs ---

type conn struct {
	pid       int
	app       string
	outbound  bool
	proto     string
	dst       string
	permitted bool
}

func (c conn) loopback() bool {
	host, _, _ := net.SplitHostPort(c.dst)
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func (c conn) key() string { return strings.ToLower(c.proto) + " " + c.dst }

func (c conn) String() string {
	dir, verdict := "inbound", "allowed"
	if c.outbound {
		dir = "outbound"
	}
	if !c.permitted {
		verdict = "blocked by firewall"
	}
	return fmt.Sprintf("%s %s %s (%s, PID %d %s)", c.proto, dir, c.dst, verdict, c.pid, filepath.Base(c.app))
}

type event struct {
	System struct {
		EventID   int `xml:"EventID"`
		Execution struct {
			ProcessID int `xml:"ProcessID,attr"`
		} `xml:"Execution"`
	} `xml:"System"`
	Data []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
	} `xml:"EventData>Data"`
}

func (e event) field(name string) string {
	for _, d := range e.Data {
		if d.Name == name {
			return d.Value
		}
	}
	return ""
}

func queryLog(logName, xpath string) ([]event, error) {
	out, err := exec.Command("wevtutil", "qe", logName, "/q:"+xpath, "/f:xml").Output()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Events []event `xml:"Event"`
	}
	if err := xml.Unmarshal([]byte("<Events>"+string(out)+"</Events>"), &doc); err != nil {
		return nil, err
	}
	return doc.Events, nil
}

func since(t time.Time) string {
	return "TimeCreated[@SystemTime>='" + t.Format("2006-01-02T15:04:05.000Z") + "']"
}

func connectionEvents(start time.Time, pids *pidSet) ([]conn, error) {
	evs, err := queryLog("Security", "*[System[(EventID=5156 or EventID=5157) and "+since(start)+"]]")
	if err != nil {
		return nil, err
	}
	var out []conn
	for _, e := range evs {
		pid, _ := strconv.Atoi(e.field("ProcessId"))
		if !pids.has(pid) {
			continue
		}
		proto := map[string]string{"6": "TCP", "17": "UDP"}[e.field("Protocol")]
		if proto == "" {
			proto = "proto " + e.field("Protocol")
		}
		outbound := e.field("Direction") == "%%14593"
		// The far end: for inbound traffic Windows lists this computer as
		// the destination, and the remote side as the source.
		remote := net.JoinHostPort(e.field("DestAddress"), e.field("DestPort"))
		if !outbound {
			remote = net.JoinHostPort(e.field("SourceAddress"), e.field("SourcePort"))
		}
		out = append(out, conn{
			pid:       pid,
			app:       e.field("Application"),
			outbound:  outbound,
			proto:     proto,
			dst:       remote,
			permitted: e.System.EventID == 5156,
		})
	}
	return out, nil
}

func dnsEvents(start time.Time, pids *pidSet) ([]string, error) {
	evs, err := queryLog(dnsLog, "*[System[EventID=3006 and "+since(start)+"]]")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range evs {
		if pids.has(e.System.Execution.ProcessID) {
			out = append(out, e.field("QueryName"))
		}
	}
	return out, nil
}

// --- processes ---

type pidSet struct {
	mu   sync.Mutex
	pids map[int]bool
}

func newPIDSet(pid int) *pidSet {
	s := &pidSet{pids: map[int]bool{pid: true}}
	s.addChildren()
	return s
}

func (s *pidSet) has(pid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pids[pid]
}

// addChildren adds every descendant of the watched processes (WebView2,
// anything the app starts).
func (s *pidSet) addChildren() {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | ForEach-Object { \"$($_.ProcessId) $($_.ParentProcessId)\" }").Output()
	if err != nil {
		return
	}
	parent := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			p, _ := strconv.Atoi(f[0])
			pp, _ := strconv.Atoi(f[1])
			parent[pp] = append(parent[pp], p)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for changed := true; changed; {
		changed = false
		for p := range s.pids {
			for _, c := range parent[p] {
				if !s.pids[c] {
					s.pids[c], changed = true, true
				}
			}
		}
	}
}

func findProcess(image string) int {
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/FO", "CSV", "/NH").Output()
	f := strings.Split(strings.TrimSpace(string(out)), "\",\"")
	if len(f) > 1 {
		pid, _ := strconv.Atoi(f[1])
		return pid
	}
	return 0
}

func isAdmin() bool {
	// "net session" only succeeds in an elevated process.
	return exec.Command("net", "session").Run() == nil
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func errText(err error) string {
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return ""
}

func fatalCode(msg string) int {
	fmt.Fprintln(os.Stderr, "leakcheck:", msg)
	return 2
}
