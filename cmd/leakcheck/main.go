// Command leakcheck verifies that nothing leaves this computer outside the
// VPN tunnel. It's part of the release checklist (RELEASING.md). Windows
// only, and it must run as administrator: it turns on Windows' own logging
// of network connections, process creation and DNS lookups for the duration,
// then puts the previous settings back.
//
//	go run ./cmd/leakcheck -selftest <magnet|file.torrent>   must report "selftest OK"
//	go run ./cmd/leakcheck [-seconds 60] <magnet|file.torrent>   must PASS
//	go run ./cmd/leakcheck -app [-seconds 150]                  must PASS
//
// The torrent form exercises the engine in VPN mode (WARP, on a throwaway
// account): it downloads the torrent, loads every provider's server list and
// searches the user's Prowlarr/Jackett if one is set up. -selftest also plants
// a short direct connection and a system DNS lookup, and succeeds only if
// both are caught.
//
// -app launches the installed app itself (as the logged-in user, not as
// administrator) after auditing is on, so its startup, first VPN connection
// and WebView2's first-launch traffic are all covered, then watches it and
// every process it starts while you use it. Quit the app before running it,
// and use WARP or a WireGuard config (a VPN account's server is only chosen
// at connect time, which this check can't know in advance).
//
// How it sees everything:
//   - Windows Filtering Platform auditing (Security events 5156/5157) records
//     every connection the OS permits or blocks, TCP and UDP, however short,
//     with the process ID.
//   - Process creation auditing (event 4688) records every process the app
//     starts, however briefly it lives.
//   - The DNS Client log (event 3006) records lookups through the system
//     resolver with the calling process.
//
// Allowed: anything to this computer (loopback), and UDP between the main
// process and the VPN server's WireGuard endpoint. Everything else, from any
// of the watched processes, is reported: including UDP from WebView2 (QUIC,
// DNS sent directly by its own resolver, WebRTC, mDNS/SSDP).
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
	wfpSubcategory     = "Filtering Platform Connection"
	processSubcategory = "Process Creation"
	dnsLog             = "Microsoft-Windows-DNS-Client/Operational"
	appImage           = "SharkBiteTorrent.exe"

	// Planted by -selftest; the check must report both.
	plantedConn   = "1.1.1.1:443"
	plantedLookup = "example.com"
)

func main() { os.Exit(run()) }

// run does the work and returns the exit code, so deferred cleanup (putting
// the auditing settings back) always happens; os.Exit would skip it.
func run() int {
	secs := flag.Int("seconds", 0, "how long to run (default 60, or 150 with -app)")
	selftest := flag.Bool("selftest", false, "plant a direct connection and a DNS lookup; succeeds only if both are caught")
	app := flag.Bool("app", false, "launch and watch the installed app instead of running the engine")
	exe := flag.String("exe", defaultExe(), "-app: the app to launch")
	endpointFlag := flag.String("endpoint", "", "-app: the VPN server's WireGuard endpoint ip:port (default: read from the app's WARP or WireGuard settings)")
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
			*secs = 150
		} else if *selftest {
			*secs = 10
		}
	}

	// The endpoint UDP may go to, from the main process only.
	var endpoint string
	var acc *warp.Account
	if *app {
		if findProcess(appImage) != 0 {
			return fatalCode("quit Shark BiteTorrent first (tray icon → Quit): this check launches it itself, to cover its startup")
		}
		if _, err := os.Stat(*exe); err != nil {
			return fatalCode("app not found at " + *exe + " (use -exe)")
		}
		endpoint = *endpointFlag
		if endpoint == "" {
			var err error
			if endpoint, err = appEndpoint(); err != nil {
				return fatalCode(err.Error())
			}
		}
	} else {
		if flag.NArg() != 1 {
			return fatalCode("usage: leakcheck [-selftest] [-seconds N] <magnet|file.torrent>   or   leakcheck -app")
		}
		// A throwaway WARP account. Registering it is a direct API call by
		// design (it's how a WARP tunnel comes to exist), so it happens
		// before the auditing starts.
		tmp, err := os.MkdirTemp("", "leakcheck")
		if err != nil {
			return fatalCode(err.Error())
		}
		defer os.RemoveAll(tmp)
		if acc, err = warp.LoadOrRegister(filepath.Join(tmp, "warp.json")); err != nil {
			return fatalCode("registering a test WARP account: " + err.Error())
		}
		endpoint = acc.Endpoint
	}
	endpoint = normalizeHostPort(endpoint)

	restore, err := startAuditing()
	if err != nil {
		return fatalCode("turning on auditing: " + err.Error())
	}
	defer restore()
	time.Sleep(time.Second) // let the audit policy take effect
	start := time.Now().UTC()

	// Control: a connection of our own (to this computer) that the audit log
	// must record. It proves auditing works, so silence from the watched
	// processes means clean rather than "not logged".
	control := controlConnection()

	var root int
	var polled *pidSet
	if *app {
		root, polled, err = runApp(*exe, *secs)
	} else {
		root = os.Getpid()
		err = runEngine(acc, *secs, *selftest, flag.Arg(0))
	}
	if err != nil {
		return fatalCode(err.Error())
	}
	time.Sleep(3 * time.Second) // let the last events reach the logs

	watched, err := descendants(start, root)
	if err != nil {
		return fatalCode("reading the process log: " + err.Error())
	}
	if polled != nil {
		watched.merge(polled)
	}
	conns, err := connectionEvents(start, watched)
	if err != nil {
		return fatalCode("reading the connection log: " + err.Error())
	}
	lookups, dnsErr := dnsEvents(start, watched)
	controlSeen := false
	if own, err := connectionEvents(start, newPIDSet(os.Getpid())); err == nil {
		for _, c := range own {
			if c.remote == control || c.local == control {
				controlSeen = true
			}
		}
	}

	var leaks []string
	seenTunnel := false
	for _, c := range conns {
		switch {
		case c.loopback():
		case c.proto == "UDP" && c.pid == root && c.remote == endpoint:
			seenTunnel = true
		default:
			l := c.String() + fmt.Sprintf(" at +%ds", int(c.at.Sub(start).Seconds()))
			if k := watched.kindOf(c.pid); k != "" {
				l += " [" + k + "]"
			}
			leaks = append(leaks, l)
		}
	}
	for _, q := range lookups {
		if !strings.EqualFold(q, "localhost") {
			leaks = append(leaks, "DNS lookup outside the tunnel: "+q)
		}
	}
	leaks = dedupe(leaks)

	fmt.Printf("\nwatched %d processes; connection events: %d; system DNS lookups: %v\n", watched.len(), len(conns), dedupe(lookups))
	if !controlSeen {
		fmt.Println("FAIL: the audit log didn't record the check's own test connection, so nothing was verified")
		return 1
	}
	if dnsErr != nil {
		fmt.Println("FAIL: couldn't read the DNS log, so DNS lookups weren't checked:", dnsErr)
		return 1
	}
	if !seenTunnel {
		// Both the engine run and -app start their tunnel during the check,
		// so its traffic must show up.
		fmt.Println("FAIL: the VPN tunnel's own traffic (UDP to " + endpoint + ") wasn't recorded, so nothing was verified")
		return 1
	}

	if *selftest {
		missed := []string{}
		if !containsAny(leaks, plantedConn) {
			missed = append(missed, "the direct connection to "+plantedConn)
		}
		if !containsAny(leaks, plantedLookup) {
			missed = append(missed, "the DNS lookup of "+plantedLookup)
		}
		if len(missed) > 0 {
			fmt.Println("selftest FAILED: the check missed", strings.Join(missed, " and "))
			return 1
		}
		fmt.Println("selftest OK: caught both planted leaks:")
		for _, l := range leaks {
			fmt.Println("  ", l)
		}
		return 0
	}

	if len(leaks) > 0 {
		fmt.Println("FAIL: traffic outside the tunnel:")
		for _, l := range leaks {
			fmt.Println("  ", l)
		}
		return 1
	}
	fmt.Println("PASS: only the VPN tunnel and this computer were contacted; no system DNS lookups")
	return 0
}

// runEngine brings up WARP on a throwaway account and exercises the engine,
// provider APIs and search, like the app does in VPN mode.
func runEngine(acc *warp.Account, secs int, selftest bool, source string) error {
	tun, err := tunnel.Start(tunnel.Config{
		PrivateKey: acc.PrivateKey, PeerPublicKey: acc.PeerPublicKey, Endpoint: acc.Endpoint,
		Addresses: []string{acc.AddressV4, acc.AddressV6}, DNS: []string{"1.1.1.1", "1.0.0.1"},
	})
	if err != nil {
		return fmt.Errorf("tunnel: %w", err)
	}
	defer tun.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tr, err := tun.CheckTrace(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	fmt.Printf("tunnel up, exit %s warp=%s\n", tr.IP, tr.Warp)
	vpnprov.SetTunnel(tun.DialContext) // as the app does once connected
	defer vpnprov.SetTunnel(nil)

	out, _ := os.MkdirTemp("", "leakcheck-dl")
	defer os.RemoveAll(out)
	eng, err := engine.New(tun, engine.Options{})
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	defer eng.Close()
	hash, err := eng.Add(engine.AddOptions{Source: source, Dir: out})
	if err != nil {
		return fmt.Errorf("add: %w", err)
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
		// two things a socket-polling check would miss.
		if c, err := net.DialTimeout("tcp", plantedConn, 10*time.Second); err == nil {
			c.Close()
		}
		(&net.Resolver{}).LookupHost(context.Background(), plantedLookup)
		fmt.Println("selftest: planted a short direct connection to", plantedConn, "and a system DNS lookup of", plantedLookup)
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
	return nil
}

// runApp launches the app as the logged-in user (through Explorer, so it
// isn't elevated like this process) and watches it for secs seconds.
func runApp(exe string, secs int) (int, *pidSet, error) {
	exec.Command("explorer.exe", exe).Run() // explorer's exit code is meaningless
	var pid int
	for i := 0; i < 60 && pid == 0; i++ {
		time.Sleep(500 * time.Millisecond)
		pid = findProcess(appImage)
	}
	if pid == 0 {
		return 0, nil, errors.New("the app didn't start")
	}
	set := newPIDSet(pid)
	fmt.Printf("Started Shark BiteTorrent (PID %d). For the next %d seconds, YOU use it: type in the search box and search, type or paste in the magnet box, open every Settings section (type in a text field, click through VPN providers without saving), and click Check for updates.\n", pid, secs)
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(deadline) {
		set.addChildren() // backup to the process-creation log
		time.Sleep(time.Second)
	}
	fmt.Println("Done watching; the app keeps running.")
	return pid, set, nil
}

func defaultExe() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Shark BiteTorrent", appImage)
}

// appEndpoint reads the WireGuard endpoint the app will connect to from its
// saved settings: the WARP account or the imported WireGuard config.
func appEndpoint() (string, error) {
	cfgDir, _ := os.UserConfigDir()
	dir := filepath.Join(cfgDir, "SharkBiteTorrent")
	var set struct {
		VpnProvider string `json:"vpnProvider"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		json.Unmarshal(data, &set)
	}
	switch set.VpnProvider {
	case "", "warp":
		data, err := secret.ReadFile(filepath.Join(dir, "warp.json"))
		if err != nil {
			return "", fmt.Errorf("can't read the app's WARP account (%v); pass -endpoint", err)
		}
		var acc warp.Account
		if err := json.Unmarshal(data, &acc); err != nil || acc.Endpoint == "" {
			return "", errors.New("can't read the app's WARP endpoint; pass -endpoint")
		}
		return acc.Endpoint, nil
	case "custom":
		data, err := secret.ReadFile(filepath.Join(dir, "wireguard.conf"))
		if err != nil {
			return "", fmt.Errorf("can't read the app's WireGuard config (%v); pass -endpoint", err)
		}
		cfg, err := tunnel.ParseWGQuick(string(data))
		if err != nil {
			return "", err
		}
		return cfg.Endpoint, nil
	case "none":
		return "", errors.New("the app is set to No VPN; switch it to WARP for this check")
	default:
		return "", errors.New("the app uses a VPN account, whose server is chosen when it connects; switch it to WARP for this check, or pass -endpoint ip:port")
	}
}

// controlConnection makes one loopback TCP connection and returns the
// address it connected to.
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

// --- auditing on/off ---

func startAuditing() (restore func(), err error) {
	type prev struct{ name, success, failure string }
	var saved []prev
	for _, sub := range []string{wfpSubcategory, processSubcategory} {
		out, err := exec.Command("auditpol", "/get", "/subcategory:"+sub, "/r").Output()
		if err != nil {
			return nil, fmt.Errorf("auditpol: %w", err)
		}
		// The CSV's Inclusion Setting: "No Auditing", "Success", "Failure" or
		// "Success and Failure".
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		setting := lines[len(lines)-1]
		saved = append(saved, prev{sub, onOff(strings.Contains(setting, "Success")), onOff(strings.Contains(setting, "Failure"))})
		if out, err := exec.Command("auditpol", "/set", "/subcategory:"+sub, "/success:enable", "/failure:enable").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("auditpol: %v: %s", err, out)
		}
	}
	prevDNS, _ := exec.Command("wevtutil", "gl", dnsLog).Output()
	dnsWasEnabled := strings.Contains(string(prevDNS), "enabled: true")
	if !dnsWasEnabled {
		exec.Command("wevtutil", "sl", dnsLog, "/e:true").Run()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, p := range saved {
				exec.Command("auditpol", "/set", "/subcategory:"+p.name, "/success:"+p.success, "/failure:"+p.failure).Run()
			}
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
	at            time.Time
	pid           int
	app           string
	outbound      bool
	proto         string
	local, remote string
	permitted     bool
}

func (c conn) loopback() bool {
	host, _, _ := net.SplitHostPort(c.remote)
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func (c conn) String() string {
	dir, verdict := "inbound from", "allowed"
	if c.outbound {
		dir = "outbound to"
	}
	if !c.permitted {
		verdict = "blocked by firewall"
	}
	return fmt.Sprintf("%s %s %s (%s, PID %d %s)", c.proto, dir, c.remote, verdict, c.pid, filepath.Base(c.app))
}

type event struct {
	System struct {
		EventID     int `xml:"EventID"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		Execution struct {
			ProcessID int `xml:"ProcessID,attr"`
		} `xml:"Execution"`
	} `xml:"System"`
	Data []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
	} `xml:"EventData>Data"`
}

// field looks a value up by name, ignoring case (Windows logs "ProcessId"
// for events 5156/5157; Microsoft's documentation writes "ProcessID").
func (e event) field(name string) string {
	for _, d := range e.Data {
		if strings.EqualFold(d.Name, name) {
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

// descendants returns root and every process started (directly or not) by
// it since start, from the process-creation log (event 4688).
func descendants(start time.Time, root int) (*pidSet, error) {
	evs, err := queryLog("Security", "*[System[EventID=4688 and "+since(start)+"]]")
	if err != nil {
		return nil, err
	}
	children := map[int][]int{}
	for _, e := range evs {
		child, err1 := parseHexPID(e.field("NewProcessId"))
		parent, err2 := parseHexPID(e.field("ProcessId"))
		if err1 == nil && err2 == nil {
			children[parent] = append(children[parent], child)
		}
	}
	set := &pidSet{pids: map[int]bool{root: true}}
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !set.pids[c] {
				set.pids[c] = true
				queue = append(queue, c)
			}
		}
	}
	return set, nil
}

func parseHexPID(s string) (int, error) {
	// Windows process IDs fit in a signed 32-bit int, so this can't overflow.
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 32)
	return int(n), err
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
			proto = "protocol " + e.field("Protocol")
		}
		outbound := e.field("Direction") == "%%14593"
		src := normalizeHostPort(net.JoinHostPort(e.field("SourceAddress"), e.field("SourcePort")))
		dst := normalizeHostPort(net.JoinHostPort(e.field("DestAddress"), e.field("DestPort")))
		// For inbound traffic Windows lists this computer as the destination.
		local, remote := src, dst
		if !outbound {
			local, remote = dst, src
		}
		at, _ := time.Parse(time.RFC3339Nano, e.System.TimeCreated.SystemTime)
		out = append(out, conn{
			at: at, pid: pid, app: e.field("Application"), outbound: outbound, proto: proto,
			local: local, remote: remote, permitted: e.System.EventID == 5156,
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

// normalizeHostPort makes "ip:port" comparable however Windows or the app
// wrote the address (IPv6 zero compression, brackets).
func normalizeHostPort(hp string) string {
	host, port, err := net.SplitHostPort(hp)
	if err != nil {
		return hp
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return net.JoinHostPort(host, port)
}

// --- processes ---

type pidSet struct {
	mu   sync.Mutex
	pids map[int]bool
	kind map[int]string // e.g. "msedgewebview2.exe --type=utility --utility-sub-type=…"
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

func (s *pidSet) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pids)
}

func (s *pidSet) merge(o *pidSet) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kind == nil {
		s.kind = map[int]string{}
	}
	for p := range o.pids {
		s.pids[p] = true
		if k := o.kind[p]; k != "" {
			s.kind[p] = k
		}
	}
}

// addChildren adds every running descendant of the watched processes.
func (s *pidSet) addChildren() {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | ForEach-Object { \"$($_.ProcessId) $($_.ParentProcessId) $($_.Name) $($_.CommandLine)\" }").Output()
	if err != nil {
		return
	}
	parent := map[int][]int{}
	kinds := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			p, _ := strconv.Atoi(f[0])
			pp, _ := strconv.Atoi(f[1])
			parent[pp] = append(parent[pp], p)
			if len(f) >= 3 {
				kinds[p] = processKind(f[2], strings.Join(f[3:], " "))
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kind == nil {
		s.kind = map[int]string{}
	}
	for p := range s.pids {
		if k, ok := kinds[p]; ok {
			s.kind[p] = k
		}
	}
	for changed := true; changed; {
		changed = false
		for p := range s.pids {
			for _, c := range parent[p] {
				if !s.pids[c] {
					s.pids[c], changed = true, true
					s.kind[c] = kinds[c]
				}
			}
		}
	}
}

// processKind is the image name plus Chromium's process-type switches.
func processKind(name, cmdline string) string {
	k := name
	for _, f := range strings.Fields(cmdline) {
		if strings.HasPrefix(f, "--type=") || strings.HasPrefix(f, "--utility-sub-type=") || strings.HasPrefix(f, "--service-sandbox-type=") {
			k += " " + f
		}
	}
	return k
}

func (s *pidSet) kindOf(pid int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kind[pid]
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

func containsAny(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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
