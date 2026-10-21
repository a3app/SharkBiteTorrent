//go:build darwin

package main

// macOS: tcpdump's pktap interface (-i pktap,all -k NP) tags every packet,
// on every interface, with the process that sent it ("proc"). The kernel
// does the tagging from the socket, so every TCP and UDP packet counts,
// however short-lived the connection, including UDP sent without connect().
// The packets are read live and counted per address, protocol and ports;
// nothing is written to disk.
//
// System DNS: lookups go to mDNSResponder, which sends the DNS packets
// itself. pktap is meant to tag those with the process they're for
// ("eproc"), but on macOS 26–27 it often doesn't (whole lookups, our own
// test lookups included, came through untagged), so DNS is attributed from
// mDNSResponder's own log instead: every request is logged with the client's
// PID. The log hides the name but gives a hash of it (<dn:…>), stable for
// the same name. The check learns the hashes of the names that are allowed
// (a VPN account's documented direct calls, and localhost) by looking them
// up itself first; any other lookup by a watched process is a leak, named if
// its hash is known. Like the Windows check, a lookup counts even when it's
// answered from the cache, without a packet. HTTPS to a documented host is
// allowed only to the addresses DNS answered for it during the run (read
// from the packets). mDNSResponder's own packets aren't judged as traffic.
//
// Watched processes: the app (or the engine run, a child process), anything
// it starts, and WebKit's helper processes working for it. Those are started
// by launchd, not the app, so they're found by asking launchd which app is
// responsible for them (launchctl procinfo). Process lists are polled once a
// second, so a child that starts, sends and exits within that is missed.
// -app fails if none of the app's WebKit processes was found.
//
// It runs as the logged-in user and uses sudo (asked once) for tcpdump, log
// stream, launchctl procinfo and flushing the DNS cache; the watched
// processes run as the user.
//
// Allowed: as on Linux (see classify): this computer except DNS, UDP to the
// VPN server, the user's local search server, the search proxy answering
// Prowlarr, the remote control's replies to the home network or Tailscale,
// and a VPN account's documented direct calls while connecting.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sharkbite/internal/systool"
	"sharkbite/internal/warp"
)

const darwinExe = "/Applications/SharkBiteTorrent.app/Contents/MacOS/SharkBiteTorrent"

func runDarwin(secs int, selftest, app bool, exe, endpointFlag, childWarp string) int {
	if childWarp != "" {
		return runChild(childWarp, secs, selftest)
	}
	if exe == defaultExe() {
		exe = darwinExe
	}
	if strings.HasSuffix(strings.TrimSuffix(exe, "/"), ".app") {
		exe = filepath.Join(exe, "Contents", "MacOS", "SharkBiteTorrent")
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
		if darwinAppRunning() {
			return fatalCode("quit Shark BiteTorrent first (Cmd+Q): this check launches it itself, to cover its startup")
		}
		if _, err := os.Stat(exe); err != nil {
			return fatalCode("app not found at " + exe + " (use -exe)")
		}
		endpoint = endpointFlag
		remotePort, remoteTailscale = appRemotePort()
		if endpoint == "" {
			var err error
			if endpoint, err = appEndpoint(); errors.Is(err, errAccountEndpoint) {
				fmt.Println("The app uses a VPN account; the server it connects to is read from the app afterwards.")
			} else if err != nil {
				return fatalCode(err.Error())
			}
		}
		childArgs = []string{exe} // opened with open(1), below
	} else {
		if len(flag.Args()) != 1 {
			return fatalCode("usage: leakcheck [-selftest] [-seconds N] <magnet|file.torrent>   or   leakcheck -app")
		}
		// A throwaway WARP account, registered here, by this process, which
		// isn't watched: registering is a direct API call by design.
		tmp, err := os.MkdirTemp("", "leakcheck")
		if err != nil {
			return fatalCode(err.Error())
		}
		defer os.RemoveAll(tmp)
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
	if endpoint != "" {
		endpoint = normalizeHostPort(endpoint)
	}

	readEndpoints := func() []string { return nil }
	if app {
		readEndpoints = startEndpointRecording()
	}
	defer func() { stopEndpointRecording() }()

	fmt.Println("YOU: if sudo asks for your password, type it (it's needed for tcpdump, the DNS log, launchctl and flushing the DNS cache).")
	if err := sudoInteractive("-v"); err != nil {
		return fatalCode("sudo: " + err.Error())
	}
	keepAlive := time.NewTicker(60 * time.Second)
	defer keepAlive.Stop()
	go func() {
		for range keepAlive.C {
			sudo("-v")
		}
	}()
	// So the documented hosts' lookups go out and their answers are seen.
	sudo("dscacheutil", "-flushcache")
	sudo("killall", "-HUP", "mDNSResponder")

	capture, err := startPktap()
	if err != nil {
		return fatalCode("starting tcpdump: " + err.Error())
	}
	defer capture.stop()
	dnsLog, err := startMDNSLog()
	if err != nil {
		return fatalCode("starting the DNS log: " + err.Error())
	}
	defer dnsLog.stop()

	ownPID := os.Getpid()
	controls := &controls{}
	controls.run() // at the start; again at the end
	learned := learnNames()

	// The app is opened the way Finder opens it (LaunchServices), not as a
	// child of this terminal: macOS then makes the app, not the terminal,
	// responsible for its WebKit helpers, which is how they're found. -W
	// makes open wait until the app quits.
	var cmd *exec.Cmd
	var appLog *os.File
	if app {
		bundle := strings.TrimSuffix(exe, "/Contents/MacOS/SharkBiteTorrent")
		if appLog, err = os.CreateTemp("", "leakcheck-app-*.log"); err != nil {
			return fatalCode(err.Error())
		}
		appLog.Close()
		defer os.Remove(appLog.Name())
		cmd = exec.Command("open", "-n", "-W", "--stdout", appLog.Name(), "--stderr", appLog.Name(), bundle)
	} else {
		cmd = exec.Command(childArgs[0], childArgs[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Ctrl+C reaches only us; we stop it
	}
	if err := cmd.Start(); err != nil {
		return fatalCode("starting the watched process: " + err.Error())
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	root := cmd.Process.Pid
	if app {
		for i := 0; i < 60 && (root == cmd.Process.Pid); i++ {
			time.Sleep(500 * time.Millisecond)
			if out, err := systool.Command("pgrep", "-x", "SharkBiteTorrent").Output(); err == nil {
				if p, err := strconv.Atoi(strings.Fields(string(out))[0]); err == nil {
					root = p
				}
			}
		}
		if root == cmd.Process.Pid {
			cmd.Process.Kill()
			return fatalCode("the app didn't start")
		}
	}
	stop := func() { // a last resort (Ctrl+C, or the app not quit in time): at once, nothing more to watch
		if app {
			syscall.Kill(root, syscall.SIGKILL)
		} else {
			syscall.Kill(-root, syscall.SIGKILL)
		}
	}
	watched := newMacWatch(root)
	tracking := make(chan struct{})
	go func() {
		for {
			watched.refresh()
			select {
			case <-tracking:
				return
			case <-time.After(time.Second):
			}
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		fmt.Println("\ninterrupted")
		stop()
		capture.stop()
		dnsLog.stop()
		stopEndpointRecording()
		os.Exit(130)
	}()
	start := time.Now()

	if app {
		fmt.Printf("Started Shark BiteTorrent (PID %d); watching it for %d seconds.\nYOU: leave the window alone for the first minute (that covers startup). Then: search and add a result; type or paste in the magnet box; open every Settings section (type in a text field, click through VPN providers without saving); turn the search proxy on and click Check it's working (then off again if you don't use it); switch to another VPN provider and save, then back (that covers reconnecting); and click Check for updates.\n", root, secs)
		select {
		case err := <-exited:
			out, _ := os.ReadFile(appLog.Name())
			return fatalCode(fmt.Sprintf("the app exited after %ds (%v): %s", int(time.Since(start).Seconds()), err, lastLines(string(out), 10)))
		case <-time.After(time.Duration(secs) * time.Second):
		}
		// Quitting is covered too.
		fmt.Println("\nYOU: now quit Shark BiteTorrent (Cmd+Q).")
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
	close(tracking)
	controls.run()
	time.Sleep(3 * time.Second) // let the last packets and log lines arrive
	res, err := capture.finish()
	if err != nil {
		return fatalCode("reading tcpdump's output: " + err.Error())
	}
	reqs := dnsLog.finish()

	fmt.Printf("\nwatched %d processes (%d WebKit helpers); tcpdump saw %d packets; DNS log: %d lookups\n", watched.len(), watched.webkitCount(), res.packets, len(reqs))
	if res.withDir == 0 {
		fmt.Println("FAIL: tcpdump didn't print packet directions (-k D), so what the app sent couldn't be told from what it received")
		return 1
	}
	if res.dropped != 0 {
		fmt.Printf("FAIL: tcpdump dropped %d packets, so not everything was checked (try fewer -seconds or a smaller torrent)\n", res.dropped)
		return 1
	}
	if missing := controls.missing(res, reqs, ownPID); len(missing) > 0 {
		fmt.Println("FAIL: the check's own test traffic wasn't all recorded (" + strings.Join(missing, ", ") + "), so the run wasn't fully covered")
		return 1
	}
	if app && watched.webkitCount() == 0 {
		fmt.Println("FAIL: none of the app's WebKit processes was found (launchctl procinfo), so its web view's traffic wasn't covered")
		return 1
	}

	endpoints := endpointSet(endpoint, readEndpoints())
	if len(endpoints) == 0 {
		return fatalCode("the app didn't record a VPN connection during the run (did it connect?); pass -endpoint ip:port")
	}
	local := localAddrs()
	flows, un := res.forWatched(watched, local, mdnsPIDs(reqs))
	fmt.Printf("untagged packets: %d claimed through their connection; unclaimed: %d ICMP (the kernel's), %d DNS (judged from the log), %d TCP/UDP\n",
		un.rescued, un.icmp, un.dns, un.unclaimed)
	if len(un.flows) > 0 {
		// Not necessarily the app's: any program's traffic can be untagged.
		fmt.Println("note: TCP/UDP that pktap tagged with no process, on connections it tagged nowhere (whose it was can't be told):")
		for _, f := range un.flows {
			fmt.Println("  ", f)
		}
	}
	var total int64
	for _, f := range flows {
		total += f.packets
	}
	pol := policy{local: local, endpoints: endpoints, remotePort: remotePort, remoteTailscale: remoteTailscale,
		lookups: watchedLookups(reqs, watched, ownPID, learned, res.answersByName())}
	pol.servers, pol.listen = appLocalServices()
	if selftest {
		res.showPlanted(watched)
	}
	return verdict(flows, total, pol, selftest)
}

// showPlanted prints every packet tcpdump saw to or from the selftest's
// planted connection, with its tags, so a missed one can be explained.
func (r *pktapResult) showPlanted(w *macWatch) {
	host, _, _ := net.SplitHostPort(plantedConn)
	for k, n := range r.counts {
		if k.dst == host || k.src == host {
			fmt.Printf("  planted connection's packets: %s %s:%d > %s:%d proc %d eproc %d dir %q watched %v: %d\n",
				k.proto, k.src, k.sport, k.dst, k.dport, k.proc, k.eproc, k.dir, w.has(k.proc) || w.has(k.eproc), n)
		}
	}
}

func darwinAppRunning() bool {
	return systool.Command("pgrep", "-x", "SharkBiteTorrent").Run() == nil
}

// timedLookup is a lookup this process made, and when, so its requests can
// be found in mDNSResponder's log (which hides the name).
type timedLookup struct {
	name     string
	from, to time.Time
}

// lookupFor looks name up from this process, alone, and returns when.
func lookupFor(name string, d time.Duration) timedLookup {
	time.Sleep(200 * time.Millisecond) // keep it apart from the previous one
	t := timedLookup{name: name, from: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	(&net.Resolver{}).LookupHost(ctx, name)
	cancel()
	t.to = time.Now()
	return t
}

// learnNames looks up, from this process, every name a watched process may
// look up by design (and the selftest's planted one), so their hashes in
// mDNSResponder's log can be named.
func learnNames() []timedLookup {
	var out []timedLookup
	for _, n := range append([]string{"localhost", plantedLookup}, sortedKeys(documentedDirect)...) {
		out = append(out, lookupFor(n, 5*time.Second))
	}
	return out
}

// --- the processes being watched ---

type macWatch struct {
	mu     sync.Mutex
	pids   map[int]bool
	asked  map[int]bool // WebKit processes already asked about
	webkit map[int]bool
}

func newMacWatch(root int) *macWatch {
	return &macWatch{pids: map[int]bool{root: true}, asked: map[int]bool{}, webkit: map[int]bool{}}
}

func (w *macWatch) has(pid int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pids[pid]
}

func (w *macWatch) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pids)
}

func (w *macWatch) webkitCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.webkit)
}

var responsiblePID = regexp.MustCompile(`(?m)^\s*responsible pid = (\d+)`)

// refresh adds the watched processes' descendants and the WebKit processes
// launchd says the app is responsible for.
func (w *macWatch) refresh() {
	out, err := systool.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return
	}
	children := map[int][]int{}
	var webkit []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		children[ppid] = append(children[ppid], pid)
		if strings.Contains(strings.Join(f[2:], " "), "com.apple.WebKit.") {
			webkit = append(webkit, pid)
		}
	}
	w.mu.Lock()
	var ask []int
	for _, p := range webkit {
		if !w.asked[p] {
			w.asked[p] = true
			ask = append(ask, p)
		}
	}
	w.mu.Unlock()
	for _, p := range ask {
		info, _ := sudo("launchctl", "procinfo", strconv.Itoa(p))
		if m := responsiblePID.FindSubmatch(info); m != nil {
			r, _ := strconv.Atoi(string(m[1]))
			w.mu.Lock()
			if w.pids[r] {
				w.pids[p], w.webkit[p] = true, true
			}
			w.mu.Unlock()
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for changed := true; changed; {
		changed = false
		for p := range w.pids {
			for _, c := range children[p] {
				if !w.pids[c] {
					w.pids[c], changed = true, true
				}
			}
		}
	}
}

// --- a privileged process whose output is read line by line ---

type lineReader struct {
	cmd    *exec.Cmd
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	stderr bytes.Buffer
}

// startLines runs sudo args, passing each stdout line to onLine and
// returning once a line (stdout or stderr) satisfies ready.
func startLines(args []string, onLine func(string), ready func(string) bool) (*lineReader, error) {
	l := &lineReader{done: make(chan struct{})}
	l.cmd = systool.Command("sudo", append([]string{"-n"}, args...)...)
	out, err := l.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errPipe, err := l.cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := l.cmd.Start(); err != nil {
		return nil, err
	}
	isReady := make(chan bool, 2)
	var readyOnce sync.Once
	signalReady := func(ok bool) { readyOnce.Do(func() { isReady <- ok }) }
	go func() {
		sc := bufio.NewScanner(errPipe)
		for sc.Scan() {
			line := sc.Text()
			l.mu.Lock()
			l.stderr.WriteString(line + "\n")
			l.mu.Unlock()
			if ready(line) {
				signalReady(true)
			}
		}
		signalReady(false)
	}()
	go func() {
		defer close(l.done)
		r := bufio.NewReaderSize(out, 1<<20)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				line = strings.TrimRight(line, "\r\n")
				if ready(line) {
					signalReady(true)
				}
				onLine(line)
			}
			if err != nil {
				if err != io.EOF {
					fmt.Fprintln(os.Stderr, "leakcheck: reading", args[0]+":", err)
				}
				signalReady(false)
				return
			}
		}
	}()
	select {
	case ok := <-isReady:
		if !ok {
			return nil, errors.New(strings.TrimSpace(l.stderrText()))
		}
	case <-time.After(15 * time.Second):
		l.stop()
		return nil, errors.New(args[0] + " didn't start")
	}
	return l, nil
}

func (l *lineReader) stderrText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stderr.String()
}

// stop ends the process (safe to call more than once).
func (l *lineReader) stop() {
	l.once.Do(func() {
		if l.cmd.Process == nil {
			return
		}
		// sudo passes the signal on (tcpdump then prints its counts).
		l.cmd.Process.Signal(os.Interrupt)
		ch := make(chan struct{})
		go func() { l.cmd.Wait(); close(ch) }()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			l.cmd.Process.Kill()
			<-ch
		}
	})
}

// --- tcpdump ---

type pktap struct {
	*lineReader
	mu  sync.Mutex
	res *pktapResult
}

// pktapKey is one counted set of packets: who sent (or received) them
// (proc) and for whom (eproc), their direction, addresses, protocol and
// ports.
type pktapKey struct {
	proc, eproc  int
	dir          string // "in" or "out"
	proto        string
	src, dst     string
	sport, dport int
}

type pktapResult struct {
	packets, dropped int64
	withDir          int64 // packets whose direction tcpdump printed
	counts           map[pktapKey]int64
	queryNames       map[string]string   // server|id → name, every DNS query seen
	answers          map[string][]string // server|id → addresses
}

func newPktapResult() *pktapResult {
	return &pktapResult{counts: map[pktapKey]int64{}, queryNames: map[string]string{}, answers: map[string][]string{}}
}

func startPktap() (*pktap, error) {
	// -l: a line at a time; -nn: numeric addresses and ports; -B: a 64 MB
	// buffer so a busy download doesn't make the kernel drop packets.
	p := &pktap{res: newPktapResult()}
	lr, err := startLines([]string{"tcpdump", "-i", "pktap,all", "-k", "NPD", "-nn", "-l", "-B", "65536", "-s", "600"},
		func(line string) {
			p.mu.Lock()
			p.res.add(line)
			p.mu.Unlock()
		},
		func(line string) bool { return strings.HasPrefix(line, "listening on") })
	if err != nil {
		return nil, err
	}
	p.lineReader = lr
	return p, nil
}

var (
	capturedRe = regexp.MustCompile(`(\d+) packets? captured`)
	droppedRe  = regexp.MustCompile(`(\d+) packets? dropped by kernel`)
)

// finish stops tcpdump and returns what it saw.
func (p *pktap) finish() (*pktapResult, error) {
	p.stop()
	<-p.done
	errText := p.stderrText()
	m := capturedRe.FindStringSubmatch(errText)
	d := droppedRe.FindStringSubmatch(errText)
	if m == nil || d == nil {
		return nil, errors.New("tcpdump didn't report its packet counts: " + lastLines(errText, 5))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.res.packets, _ = strconv.ParseInt(m[1], 10, 64)
	p.res.dropped, _ = strconv.ParseInt(d[1], 10, 64)
	return p.res, nil
}

// kernelPID stands for kernel_task, whose real PID (0) means "untagged" here.
const kernelPID = -1

var (
	// 00:59:12.522988 (proc mDNSResponder:656, eproc dscacheutil:17455) IP 100.72.141.16.53153 > 100.100.100.100.53: 48106+ AAAA? example.com. (46)
	pktapLine = regexp.MustCompile(`^\S+ \((.*?)\) (IP6?) (\S+) > (\S+?): (.*)$`)
	procRe    = regexp.MustCompile(`(?:^|, )proc (.*?):(\d+)`)
	eprocRe   = regexp.MustCompile(`(?:^|, )eproc (.*?):(\d+)`)
	dirRe     = regexp.MustCompile(`(?:^|, )(in|out)$`)                                                   // "(in)", "(proc x:1, out)"
	dnsQ      = regexp.MustCompile(`(?:^|: )(\d+)\+?(?: \[[^\]]*\])? ([A-Za-z0-9]+)\? (\S+) \(\d+\)\s*$`) // "Type65?" too
	dnsR      = regexp.MustCompile(`(?:^|: )(\d+)[*|$-]* \d+/\d+/\d+(?: (.*?))? \(\d+\)\s*$`)
	dnsAddr   = regexp.MustCompile(`\b(?:A|AAAA) ([0-9A-Fa-f:.]+)`)
)

// add counts one line of tcpdump's output.
func (r *pktapResult) add(line string) {
	m := pktapLine.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
	if m == nil {
		return // ARP, a continuation line, …
	}
	k := pktapKey{}
	if pm := procRe.FindStringSubmatch(m[1]); pm != nil {
		k.proc, _ = strconv.Atoi(pm[2])
		if k.proc == 0 && pm[1] != "" {
			k.proc = kernelPID // "kernel_task:0": tagged, with the kernel
		}
	}
	if em := eprocRe.FindStringSubmatch(m[1]); em != nil {
		k.eproc, _ = strconv.Atoi(em[2])
	}
	if dm := dirRe.FindStringSubmatch(m[1]); dm != nil {
		k.dir = dm[1]
		r.withDir++
	}
	var hasPorts bool
	k.src, k.sport, hasPorts = splitPktapAddr(m[3], m[2] == "IP6")
	k.dst, k.dport, _ = splitPktapAddr(m[4], m[2] == "IP6")
	rest := m[5]
	switch {
	case strings.Contains(rest, "Flags ["):
		k.proto = "tcp"
	case strings.HasPrefix(rest, "ICMP6"):
		k.proto, k.sport, k.dport = "ipv6-icmp", 0, 0
	case strings.HasPrefix(rest, "ICMP"):
		k.proto, k.sport, k.dport = "icmp", 0, 0
	case hasPorts:
		k.proto = "udp"
	default:
		k.proto = "other"
	}
	r.counts[k]++
	if q := dnsQ.FindStringSubmatch(rest); q != nil && k.dport == 53 {
		r.queryNames[k.dst+"|"+q[1]] = strings.ToLower(strings.TrimSuffix(q[3], "."))
	} else if a := dnsR.FindStringSubmatch(rest); a != nil && k.sport == 53 {
		key := k.src + "|" + a[1]
		for _, am := range dnsAddr.FindAllStringSubmatch(a[2], -1) {
			r.answers[key] = append(r.answers[key], strings.TrimSuffix(am[1], ","))
		}
	}
}

// answersByName is the addresses DNS returned for each name during the run,
// whoever asked.
func (r *pktapResult) answersByName() map[string][]string {
	out := map[string][]string{}
	for key, name := range r.queryNames {
		out[name] = append(out[name], r.answers[key]...)
	}
	return out
}

// splitPktapAddr splits tcpdump's "1.2.3.4.443" or "2600::1.443" (the port
// after the last dot) into address and port; ICMP and the like have none.
func splitPktapAddr(s string, v6 bool) (addr string, port int, ok bool) {
	s = strings.TrimSuffix(s, ":")
	if i := strings.Index(s, "%"); i >= 0 && v6 { // fe80::1%en0.5353
		if j := strings.LastIndex(s, "."); j > i {
			s = s[:i] + s[j:]
		} else {
			s = s[:i]
		}
	}
	i := strings.LastIndex(s, ".")
	if v6 {
		if i < 0 || i < strings.LastIndex(s, ":") {
			return s, 0, false
		}
	} else if strings.Count(s, ".") != 4 {
		return s, 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return s, 0, false
	}
	return s[:i], p, true
}

// connKey is a connection: protocol and both ends, the same whichever way
// a packet goes.
type connKey struct {
	proto  string
	a, b   string
	ap, bp int
}

func connOf(k pktapKey) connKey {
	if k.src > k.dst || k.src == k.dst && k.sport > k.dport {
		return connKey{k.proto, k.dst, k.src, k.dport, k.sport}
	}
	return connKey{k.proto, k.src, k.dst, k.sport, k.dport}
}

// untagged is what forWatched couldn't attribute: packets sent with no
// process tag, on connections none of whose packets had one either.
type untagged struct {
	rescued   int64 // untagged packets claimed through a tagged packet on their connection
	unclaimed int64 // untagged TCP/UDP (not DNS) to another computer, on a connection nobody's tag claims
	flows     []string
	// Untagged and unclaimed too, but not what a leak by the app would be:
	// ICMP is sent by the kernel (neighbour discovery, errors), and DNS is
	// judged from mDNSResponder's log.
	icmp, dns int64
}

// forWatched is the packets the watched processes sent as flows (packets
// tagged with them, as proc or eproc, and untagged packets on a connection
// some packet of which was tagged with them). pktap leaves some packets
// untagged, often a connection's outgoing ones while tagging the replies.
// Packets tagged with another process never count: on loopback, both ends of
// a connection are on this computer. mDNSResponder's packets are left out:
// lookups are judged from its log.
func (r *pktapResult) forWatched(w *macWatch, local map[string]bool, mdns map[int]bool) ([]flow, untagged) {
	ours := map[connKey]bool{}
	tagged := map[connKey]bool{}
	for k := range r.counts {
		if k.proc != 0 || k.eproc != 0 {
			tagged[connOf(k)] = true
		}
		if !mdns[k.proc] && (w.has(k.proc) || w.has(k.eproc)) {
			ours[connOf(k)] = true
		}
	}
	var flows []flow
	var u untagged
	for k, n := range r.counts {
		// On loopback each packet is seen leaving and arriving, the arriving
		// copy tagged with the process receiving it: only what's sent counts.
		if k.dir == "in" || !local[k.src] || mdns[k.proc] {
			continue
		}
		c := connOf(k)
		switch {
		case w.has(k.proc) || w.has(k.eproc):
		case k.proc == 0 && k.eproc == 0 && ours[c]:
			u.rescued += n
		case k.proc == 0 && k.eproc == 0 && !tagged[c] && !local[k.dst] && !multicast(k.dst):
			switch {
			case k.proto == "icmp" || k.proto == "ipv6-icmp":
				u.icmp += n
			case k.dport == 53:
				u.dns += n
			default:
				u.unclaimed += n
				u.flows = append(u.flows, fmt.Sprintf("%s %s > %s (%d packets)", strings.ToUpper(k.proto),
					net.JoinHostPort(k.src, strconv.Itoa(k.sport)), net.JoinHostPort(k.dst, strconv.Itoa(k.dport)), n))
			}
			continue
		default:
			continue // someone else's
		}
		flows = append(flows, flow{proto: k.proto, src: k.src, dst: k.dst, sport: k.sport, dport: k.dport, packets: n})
	}
	sort.Strings(u.flows)
	return flows, u
}

func multicast(addr string) bool {
	ip := net.ParseIP(addr)
	return ip != nil && (ip.IsMulticast() || ip.Equal(net.IPv4bcast) || strings.HasSuffix(addr, ".255"))
}

// --- mDNSResponder's log ---

type mdnsLog struct {
	*lineReader
	mu   sync.Mutex
	reqs []mdnsRequest
}

// mdnsRequest is one lookup a client asked mDNSResponder for.
type mdnsRequest struct {
	at      time.Time
	mdnsPID int
	client  int
	hash    string // <dn:…>, or the name itself if the log shows it
}

// [R6739] DNSServiceQueryRecord START -- qname: <private> <dn:72K6SQ>, qtype: A, flags: 0x15000, interface index: 0, client pid: 16734 (SharkBiteTorren), name hash: <dn:72K6SQ>
var (
	mdnsStart = regexp.MustCompile(`mDNSResponder\[(\d+)[^\]]*\].*\] DNSService\w+ START -- (?:qname|hostname): (.*?), .*client pid: (\d+)`)
	mdnsHash  = regexp.MustCompile(`<dn:([^>]+)>`)
)

func startMDNSLog() (*mdnsLog, error) {
	l := &mdnsLog{}
	lr, err := startLines([]string{"log", "stream", "--style", "compact", "--level", "default",
		"--predicate", `process == "mDNSResponder" AND eventMessage CONTAINS " START -- "`},
		func(line string) {
			if r, ok := parseMDNSLine(line); ok {
				l.mu.Lock()
				l.reqs = append(l.reqs, r)
				l.mu.Unlock()
			}
		},
		func(line string) bool { return strings.HasPrefix(line, "Filtering the log data") })
	if err != nil {
		return nil, err
	}
	l.lineReader = lr
	return l, nil
}

func parseMDNSLine(line string) (mdnsRequest, bool) {
	m := mdnsStart.FindStringSubmatch(line)
	if m == nil {
		return mdnsRequest{}, false
	}
	r := mdnsRequest{}
	if len(line) >= 23 {
		// "2026-10-21 00:59:24.799 Df mDNSResponder[656:145d] …", local time
		r.at, _ = time.ParseInLocation("2006-01-02 15:04:05.000", line[:23], time.Local)
	}
	r.mdnsPID, _ = strconv.Atoi(m[1])
	r.client, _ = strconv.Atoi(m[3])
	if h := mdnsHash.FindStringSubmatch(m[2]); h != nil {
		r.hash = h[1]
	} else {
		r.hash = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(m[2]), "."))
	}
	return r, true
}

func (l *mdnsLog) finish() []mdnsRequest {
	l.stop()
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reqs
}

func mdnsPIDs(reqs []mdnsRequest) map[int]bool {
	m := map[int]bool{}
	for _, r := range reqs {
		m[r.mdnsPID] = true
	}
	if out, err := systool.Command("pgrep", "-x", "mDNSResponder").Output(); err == nil {
		for _, f := range strings.Fields(string(out)) {
			if p, err := strconv.Atoi(f); err == nil {
				m[p] = true
			}
		}
	}
	return m
}

// hashOf is the hash of t's name: that of the first request this process
// made while looking it up ("" if there was none, e.g. localhost answered
// without asking mDNSResponder).
func hashOf(reqs []mdnsRequest, own int, t timedLookup) string {
	const slack = 50 * time.Millisecond // the log's timestamps are to the millisecond
	for _, r := range reqs {
		if r.client == own && !r.at.Before(t.from.Add(-slack)) && !r.at.After(t.to.Add(slack)) {
			return r.hash
		}
	}
	return ""
}

// watchedLookups names the watched processes' lookups, using the hashes of
// the names this process looked up (learned), and gives each documented one
// the addresses DNS returned for it. localhost isn't counted: it's answered
// on this computer.
func watchedLookups(reqs []mdnsRequest, w *macWatch, own int, learned []timedLookup, answers map[string][]string) map[string][]string {
	names := map[string]string{} // hash → name
	for _, t := range learned {
		if h := hashOf(reqs, own, t); h != "" {
			names[h] = t.name
		}
	}
	out := map[string][]string{}
	for _, r := range reqs {
		if !w.has(r.client) {
			continue
		}
		name := names[r.hash]
		if name == "" {
			if strings.Contains(r.hash, ".") || r.hash == "localhost" {
				name = r.hash // the log showed the name itself
			} else {
				name = "a name the check doesn't know (hash dn:" + r.hash + ")"
			}
		}
		if name == "localhost" {
			continue
		}
		out[name] = dedupe(append(out[name], answers[name]...))
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- the check's own test traffic ---

// controls are a UDP packet to this computer and a system DNS lookup of a
// name nobody else asks for, sent by this process at the start and again at
// the end. tcpdump must have recorded the packets, tagged with this process,
// and mDNSResponder's log the lookups, with this process as the client: that
// proves both worked, and attributed traffic, for the whole run.
type controls struct {
	packets []string // "127.0.0.1:9 from 127.0.0.1:port"
	lookups []timedLookup
}

func (c *controls) run() {
	if conn, err := net.Dial("udp", "127.0.0.1:9"); err == nil {
		conn.Write([]byte("leakcheck"))
		c.packets = append(c.packets, conn.RemoteAddr().String()+" from "+conn.LocalAddr().String())
		conn.Close()
	}
	name := "leakcheck-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".example.com"
	c.lookups = append(c.lookups, lookupFor(name, 5*time.Second))
}

// missing returns the controls that weren't recorded.
func (c *controls) missing(r *pktapResult, reqs []mdnsRequest, own int) []string {
	var out []string
	for _, s := range c.packets {
		var dst, src string
		fmt.Sscanf(s, "%s from %s", &dst, &src)
		found := false
		for k := range r.counts {
			if k.proc == own && k.proto == "udp" && net.JoinHostPort(k.dst, strconv.Itoa(k.dport)) == dst &&
				net.JoinHostPort(k.src, strconv.Itoa(k.sport)) == src {
				found = true
			}
		}
		if !found {
			out = append(out, "the UDP packet to "+dst+" in tcpdump")
		}
	}
	for _, t := range c.lookups {
		if hashOf(reqs, own, t) == "" {
			out = append(out, "the lookup of "+t.name+" in the DNS log")
		}
	}
	return out
}
