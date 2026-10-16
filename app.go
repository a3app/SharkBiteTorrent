package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sharkbite/internal/engine"
	"sharkbite/internal/platform"
	"sharkbite/internal/scan"
	"sharkbite/internal/secret"
	"sharkbite/internal/socks"
	"sharkbite/internal/tunnel"
	"sharkbite/internal/vpnprov"
	"sharkbite/internal/warp"
)

type App struct {
	ctx       context.Context
	configDir string
	exe       string

	mu          sync.Mutex
	tun         *tunnel.Tunnel
	eng         *engine.Engine
	stopMonitor context.CancelFunc
	vpn         VpnStatus
	saved       []savedTorrent
	settings    Settings
	pending     []string // magnets/.torrent paths received before the VPN was up
	quitting    bool
	scanning    map[string]bool // torrents being scanned right now
	proxy       *socks.Server   // local SOCKS5 proxy into the tunnel, for Prowlarr/Jackett
	proxyErr    string
	// connMu serializes connecting and reconnecting, so the startup connect
	// and quick successive Reconnects can't interleave. reconnectGen lets a
	// queued Reconnect see that a newer one supersedes it.
	remote       *remoteServer // phone remote control (remote.go)
	remoteErr    string
	remotePaused string     // why it isn't running although enabled
	remoteOpMu   sync.Mutex // serializes starting/stopping it
	remoteMu     sync.Mutex // guards store/storeErr
	store        remoteStore
	storeErr     error
	shuttingDown bool
	connMu       sync.Mutex
	reconnectGen atomic.Uint64
	sfvChecking  map[string]bool              // torrents whose .sfv is being checked
	sfvResults   map[string]*engine.SFVStatus // .sfv results not yet in a saved scan record
	verifyCtx    context.Context              // cancelled on disconnect; stops "Verify files"
}

type VpnStatus struct {
	State    string `json:"state"` // connecting, connected, error, off ("No VPN" chosen)
	Provider string `json:"provider"`
	Name     string `json:"name"`   // display name: "WARP", "Mullvad", "WireGuard"
	Server   string `json:"server"` // server picked by an account provider
	ExitIP   string `json:"exitIP"`
	Location string `json:"location"`
	Colo     string `json:"colo"`
	Port     int    `json:"port"`    // forwarded port accepting inbound peers, 0 if none
	Warning  string `json:"warning"` // non-fatal problem, e.g. port forwarding failed
	Error    string `json:"error"`
}

type Settings struct {
	DownloadDir   string `json:"downloadDir"`
	VpnProvider   string `json:"vpnProvider"`   // "warp", "account", "custom" or "none" (no VPN)
	ForwardedPort int    `json:"forwardedPort"` // custom only; accounts forward automatically
	// AccountProvider/AccountRegion apply when VpnProvider is "account".
	// An empty region means a server in the user's own country.
	AccountProvider string `json:"accountProvider"`
	AccountRegion   string `json:"accountRegion"`
	DownLimitKB     int64  `json:"downLimitKB"` // 0 = unlimited
	UpLimitKB       int64  `json:"upLimitKB"`
	CloseToTray     bool   `json:"closeToTray"`
	// Update checks (see updater.go).
	AutoUpdateCheck bool   `json:"autoUpdateCheck"`
	SkippedVersion  string `json:"skippedVersion"`
	LastUpdateCheck int64  `json:"lastUpdateCheck"` // unix seconds
	// ScanDownloads scans finished torrents with the system antivirus.
	ScanDownloads bool `json:"scanDownloads"`
	// Remote control from phones on the home network (remote.go); changed
	// through SetRemote, not SaveSettings.
	RemoteEnabled   bool `json:"remoteEnabled"`
	RemotePort      int  `json:"remotePort"`
	RemoteTailscale bool `json:"remoteTailscale"` // also listen on the Tailscale address
}

// savedTorrent is persisted so torrents survive restarts. Source is a magnet
// link or a path to a .torrent copy inside the config dir.
type savedTorrent struct {
	Hash    string `json:"hash"`
	Source  string `json:"source"`
	Dir     string `json:"dir"`
	Skipped []int  `json:"skipped,omitempty"`
	Paused  bool   `json:"paused"`
	// Scan is the antivirus result once the torrent finished (nil = not yet).
	Scan *scanRecord `json:"scan,omitempty"`
}

func NewApp() *App {
	dir, _ := os.UserConfigDir()
	exe, _ := os.Executable()
	configDir := filepath.Join(dir, "SharkBiteTorrent")
	// Carry over data from before the app was renamed.
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		os.Rename(filepath.Join(dir, "torrent-client"), configDir)
	}
	return &App{configDir: configDir, exe: exe}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadSettings()
	a.loadSaved()
	a.handleArgs(os.Args[1:])
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) { a.handleArgs(paths) })
	go a.runTray()
	go a.connect()
	a.startProxy()
	a.loadRemoteStore()
	a.remoteOpMu.Lock()
	a.startRemote()
	a.remoteOpMu.Unlock()
	go a.watchRemoteNetwork()
}

func (a *App) isShuttingDown() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quitting || a.shuttingDown
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	a.shuttingDown = true // a Reconnect in progress must not start anything new
	a.mu.Unlock()
	a.stopProxy()
	a.remoteOpMu.Lock()
	a.stopRemote()
	a.remoteOpMu.Unlock()
	a.disconnect()
	quitTray()
}

// beforeClose hides to the tray instead of quitting, if enabled.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	hide := a.settings.CloseToTray && !a.quitting
	a.mu.Unlock()
	if hide {
		runtime.WindowHide(ctx)
	}
	return hide
}

func (a *App) quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	runtime.Quit(a.ctx)
}

func (a *App) showWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowSetAlwaysOnTop(a.ctx, true) // bring to front on Windows
	runtime.WindowSetAlwaysOnTop(a.ctx, false)
}

// onSecondInstance receives the args of a second launch, e.g. when a magnet
// link is clicked in the browser while the app is already running.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	a.handleArgs(data.Args)
	if a.ctx != nil {
		a.showWindow()
	}
}

// handleArgs adds magnet links and .torrent files passed on the command line,
// via the file association, or dropped on the window.
func (a *App) handleArgs(args []string) {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "magnet:") && !strings.EqualFold(filepath.Ext(arg), ".torrent") {
			continue
		}
		a.mu.Lock()
		ready := a.eng != nil
		if !ready {
			a.pending = append(a.pending, arg)
		}
		a.mu.Unlock()
		if ready {
			if err := a.addSource(arg); err != nil {
				a.notifyError(err)
			}
		}
	}
}

func (a *App) notifyError(err error) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "error", err.Error())
	}
}

func (a *App) setVpn(s VpnStatus) {
	a.mu.Lock()
	a.vpn = s
	a.mu.Unlock()
}

// connect starts the configured VPN, verifies it, then starts the torrent
// engine. Nothing is downloaded until this succeeds.
func (a *App) connect() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.connectPrepared(nil)
}

// preparedConn is what connecting needs from outside the tunnel being
// built: the real IP (to confirm the new tunnel's exit differs) and, for
// VPN accounts, the provider's connection details.
type preparedConn struct {
	set          Settings // what it was prepared for
	account      string   // and with which account (see accountKey)
	realIP, home string
	conn         *vpnprov.Connection
}

// prepareConn does the steps of connecting that talk to the provider's API
// (sign-in token, server choice, key registration). Provider API calls go
// through whatever tunnel is up at the time (vpnprov.SetTunnel), so
// Reconnect runs this before closing the old tunnel.
func (a *App) prepareConn(set Settings) (*preparedConn, error) {
	p := &preparedConn{set: set, account: accountKey(a.account(set.AccountProvider))}
	// Only WARP reports warp=on, so for other providers we confirm the tunnel
	// by checking that the exit IP differs from the real one. The real
	// connection's country also picks the automatic server location.
	if set.VpnProvider != "warp" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if tr, err := tunnel.DirectTrace(ctx); err == nil {
			p.realIP, p.home = tr.IP, tr.Location
		}
		cancel()
	}
	if set.VpnProvider == "account" {
		prov, ok := vpnprov.Get(set.AccountProvider)
		acc := a.account(set.AccountProvider)
		if !ok || acc == nil {
			return nil, errors.New("not signed in to a VPN account; open Settings to sign in")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		c, err := prov.Connect(ctx, acc, set.AccountRegion, p.home)
		if err != nil {
			return nil, err
		}
		p.conn = c
	}
	return p, nil
}

// accountKey identifies a signed-in account, so a connection prepared with
// one account isn't used after the user signed in to another.
func accountKey(acc *vpnprov.Account) string {
	if acc == nil {
		return ""
	}
	data, _ := json.Marshal(acc.Data)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// connectPrepared is connect, optionally with prepareConn already done.
// Callers hold connMu.
func (a *App) connectPrepared(prep *preparedConn) {
	a.mu.Lock()
	if a.eng != nil || a.shuttingDown {
		a.mu.Unlock()
		return
	}
	set := a.settings
	if set.VpnProvider == "none" {
		a.vpn = VpnStatus{State: "connecting", Provider: "none", Name: "No VPN"}
		a.mu.Unlock()
		a.connectDirect(set)
		return
	}
	name := "WARP"
	switch set.VpnProvider {
	case "custom":
		name = "WireGuard"
	case "account":
		name = set.AccountProvider
		if p, ok := vpnprov.Get(set.AccountProvider); ok {
			name = p.Info().Name
		}
	}
	a.vpn = VpnStatus{State: "connecting", Provider: set.VpnProvider, Name: name}
	a.mu.Unlock()
	fail := func(err error) {
		a.setVpn(VpnStatus{State: "error", Provider: set.VpnProvider, Name: name, Error: err.Error()})
	}

	if prep != nil && (prep.set.VpnProvider != set.VpnProvider ||
		prep.set.AccountProvider != set.AccountProvider || prep.set.AccountRegion != set.AccountRegion ||
		prep.account != accountKey(a.account(set.AccountProvider))) {
		prep = nil // settings changed meanwhile
	}
	if prep == nil {
		var err error
		if prep, err = a.prepareConn(set); err != nil {
			fail(err)
			return
		}
	}
	realIP, conn := prep.realIP, prep.conn

	var cfg tunnel.Config
	switch set.VpnProvider {
	case "custom":
		data, err := secret.ReadFile(a.customConfPath())
		if err != nil {
			fail(errors.New("no WireGuard config imported; open Settings to import one"))
			return
		}
		if cfg, err = tunnel.ParseWGQuick(string(data)); err != nil {
			fail(err)
			return
		}
	case "account":
		cfg = conn.Config
	default:
		acc, err := warp.LoadOrRegister(a.warpPath())
		if err != nil {
			fail(err)
			return
		}
		cfg = warpConfig(acc)
	}

	tun, tr, err := startTunnel(cfg, set.VpnProvider, realIP)
	if err != nil && set.VpnProvider != "account" && set.VpnProvider != "custom" {
		// Cloudflare may have dropped the saved WARP device (rotated endpoint,
		// pruned inactive account). If a fresh registration works, use it.
		if acc, rerr := warp.Reregister(a.warpPath()); rerr == nil {
			runtime.LogWarningf(a.ctx, "WARP: saved device failed (%v); registered a new one", err)
			tun, tr, err = startTunnel(warpConfig(acc), set.VpnProvider, realIP)
		}
	}
	if err != nil {
		fail(err)
		return
	}

	port := 0
	var pfWarning string
	var keepalive func(context.Context) error
	var every time.Duration
	switch {
	case set.VpnProvider == "custom":
		port = set.ForwardedPort
	case conn != nil && conn.PortForward != nil:
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		p, keep, ev, err := conn.PortForward(ctx, tun)
		cancel()
		if err != nil {
			// Not fatal: downloading still works, peers just can't connect in.
			pfWarning = "port forwarding unavailable: " + err.Error()
		} else {
			port, keepalive, every = p, keep, ev
		}
	}
	eng, err := engine.New(tun, engine.Options{
		ForwardedPort: port,
		DownLimit:     set.DownLimitKB * 1024,
		UpLimit:       set.UpLimitKB * 1024,
		StateDir:      a.configDir, // download progress database
	})
	if err != nil {
		tun.Close()
		fail(err)
		return
	}

	st := VpnStatus{State: "connected", Provider: set.VpnProvider, Name: name,
		ExitIP: tr.IP, Location: tr.Location, Colo: tr.Colo, Port: port, Warning: pfWarning}
	if conn != nil {
		st.Server = conn.Server
	}
	mctx := a.startEngine(tun, eng, st)
	go a.monitor(mctx, tun)
	if keepalive != nil {
		go a.keepPortForward(mctx, keepalive, every)
	}
}

// connectDirect starts the engine on the normal network, for "No VPN". It
// only runs when the user picked that; a failing VPN never falls back here.
func (a *App) connectDirect(set Settings) {
	eng, err := engine.New(nil, engine.Options{
		DownLimit: set.DownLimitKB * 1024,
		UpLimit:   set.UpLimitKB * 1024,
		StateDir:  a.configDir,
	})
	if err != nil {
		a.setVpn(VpnStatus{State: "error", Provider: "none", Name: "No VPN", Error: err.Error()})
		return
	}
	a.startEngine(nil, eng, VpnStatus{State: "off", Provider: "none", Name: "No VPN"})
}

// startEngine records the running engine, restores saved and pending
// torrents and starts background work. The returned context ends when the
// engine is stopped.
func (a *App) startEngine(tun *tunnel.Tunnel, eng *engine.Engine, st VpnStatus) context.Context {
	mctx, stop := context.WithCancel(context.Background())
	a.mu.Lock()
	a.tun, a.eng, a.stopMonitor = tun, eng, stop
	a.verifyCtx = mctx
	if tun != nil {
		vpnprov.SetTunnel(tun.DialContext) // server lists and sign-ins go through the VPN
	}
	a.vpn = st
	saved := append([]savedTorrent(nil), a.saved...)
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()

	for _, s := range saved {
		_, err := eng.Add(engine.AddOptions{Source: s.Source, Dir: s.Dir, Skipped: s.Skipped, Paused: s.Paused})
		if err != nil {
			runtime.LogWarningf(a.ctx, "restore %s: %v", s.Hash, err)
		}
	}
	for _, src := range pending {
		if err := a.addSource(src); err != nil {
			a.notifyError(err)
		}
	}
	go a.autoCheckUpdate()
	go a.scanLoop(mctx)
	return mctx
}

func (a *App) warpPath() string { return filepath.Join(a.configDir, "warp.json") }

func warpConfig(acc *warp.Account) tunnel.Config {
	return tunnel.Config{
		PrivateKey:    acc.PrivateKey,
		PeerPublicKey: acc.PeerPublicKey,
		Endpoint:      acc.Endpoint,
		Addresses:     []string{acc.AddressV4, acc.AddressV6},
		DNS:           []string{"1.1.1.1", "1.0.0.1"},
	}
}

// startTunnel brings the tunnel up and confirms traffic leaves through the
// VPN. The engine can only reach the network through the tunnel either way;
// this check is what turns a dead or misrouted tunnel into a clear error.
func startTunnel(cfg tunnel.Config, provider, realIP string) (*tunnel.Tunnel, *tunnel.Trace, error) {
	tun, err := tunnel.Start(cfg)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tr, err := tun.CheckTrace(ctx)
	cancel()
	switch {
	case err != nil:
		err = fmt.Errorf("VPN not passing traffic: %w", err)
	case provider != "account" && provider != "custom" && tr.Warp != "on" && tr.Warp != "plus":
		err = fmt.Errorf("tunnel is up but Cloudflare reports warp=%q", tr.Warp)
	case tr.IP == "":
		err = errors.New("VPN check failed: Cloudflare's trace page didn't report an exit IP")
	case realIP != "" && tr.IP == realIP:
		err = errors.New("VPN exit IP is the same as your real IP")
	}
	if err != nil {
		tun.Close()
		return nil, nil, err
	}
	return tun, tr, nil
}

// keepPortForward refreshes a provider's port mapping; PIA drops it if not
// renewed every 15 minutes.
func (a *App) keepPortForward(ctx context.Context, keep func(context.Context) error, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := keep(cctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, vpnprov.ErrRenew) {
			// The port's signature is about to expire; only a fresh connection gets a new one.
			a.Reconnect()
			return
		}
		a.mu.Lock()
		if err != nil {
			a.vpn.Warning = "port forwarding refresh failed: " + err.Error()
		} else if strings.HasPrefix(a.vpn.Warning, "port forwarding refresh failed") {
			a.vpn.Warning = ""
		}
		a.mu.Unlock()
	}
}

func (a *App) disconnect() {
	a.mu.Lock()
	eng, tun, stop := a.eng, a.tun, a.stopMonitor
	a.eng, a.tun, a.stopMonitor = nil, nil, nil
	a.mu.Unlock()
	vpnprov.SetTunnel(nil)
	if stop != nil {
		stop()
	}
	if eng != nil {
		eng.Close()
	}
	if tun != nil {
		tun.Close()
	}
}

// monitor re-checks the exit IP every minute so the UI reflects reality.
func (a *App) monitor(ctx context.Context, tun *tunnel.Tunnel) {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		tr, err := tun.CheckTrace(cctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		if err != nil {
			a.vpn.Error = "last check failed: " + err.Error()
		} else {
			a.vpn.ExitIP, a.vpn.Location, a.vpn.Colo, a.vpn.Error = tr.IP, tr.Location, tr.Colo, ""
		}
		a.mu.Unlock()
	}
}

func (a *App) engine() (*engine.Engine, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.eng == nil {
		return nil, errors.New("VPN not connected yet")
	}
	return a.eng, nil
}

// --- Torrents (bound to the frontend) ---

func (a *App) GetVpnStatus() VpnStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vpn
}

// Reconnect tears down the VPN and engine and starts them again with the
// current settings. Torrents resume where they left off.
func (a *App) Reconnect() {
	gen := a.reconnectGen.Add(1)
	go func() {
		a.connMu.Lock()
		defer a.connMu.Unlock()
		superseded := func() bool { return a.reconnectGen.Load() != gen }
		if superseded() {
			return // a newer Reconnect is queued and will do it
		}
		// Talk to the (new) provider's API through the current tunnel while
		// it's still up, instead of over the normal connection after closing
		// it. If that fails (the old tunnel may be why we're reconnecting),
		// connect prepares again on its own.
		a.mu.Lock()
		set, hadTunnel := a.settings, a.tun != nil
		a.mu.Unlock()
		var prep *preparedConn
		if hadTunnel && set.VpnProvider != "none" {
			a.mu.Lock()
			before := a.vpn
			a.vpn.State, a.vpn.Error = "connecting", "" // switching; torrents keep running meanwhile
			a.mu.Unlock()
			var err error
			prep, err = a.prepareConn(set)
			if superseded() || a.isShuttingDown() {
				// The settings changed again while preparing (leave the old
				// tunnel up so the newer Reconnect prepares through it too),
				// or the app is quitting. Put the real status back first, so
				// the next Reconnect saves that rather than "connecting".
				a.mu.Lock()
				a.vpn = before
				a.mu.Unlock()
				return
			}
			if err != nil && a.tunnelWorks() {
				// Don't close a working VPN and then contact the new
				// provider over the normal connection instead: stay on the
				// current VPN and say why the switch didn't happen.
				a.mu.Lock()
				a.vpn = before
				a.vpn.Warning = "couldn't switch VPN through the current connection: " + err.Error() + " (still connected as before; Retry, or restart the app to connect directly)"
				a.mu.Unlock()
				return
			}
			// If preparing failed because the current tunnel is dead, there's
			// no working VPN to protect: connect like the first time (prep is
			// nil, so connecting prepares again once the tunnel is closed).
		} else if a.isShuttingDown() {
			return
		}
		a.disconnect()
		a.connectPrepared(prep)
	}()
}

// tunnelWorks reports whether the current tunnel still passes traffic.
func (a *App) tunnelWorks() bool {
	a.mu.Lock()
	tun := a.tun
	a.mu.Unlock()
	if tun == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tr, err := tun.CheckTrace(ctx)
	return err == nil && tr.IP != ""
}

func (a *App) ListTorrents() []engine.Status {
	eng, err := a.engine()
	if err != nil {
		return []engine.Status{}
	}
	return a.withScans(eng.List())
}

func (a *App) AddMagnet(link string) error {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "magnet:") {
		return errors.New("not a magnet link")
	}
	return a.addSource(link)
}

// PickTorrentFile opens a file dialog and adds the chosen .torrent files.
func (a *App) PickTorrentFile() error {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Add torrent files",
		Filters: []runtime.FileFilter{{DisplayName: "Torrent files (*.torrent)", Pattern: "*.torrent"}},
	})
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := a.addSource(p); err != nil {
			return err
		}
	}
	return nil
}

// addSource adds a magnet link or .torrent path into the current download folder.
func (a *App) addSource(src string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(src, "magnet:") {
		mi, err := metainfo.LoadFromFile(src)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(src), err)
		}
		// Keep our own copy so the torrent survives the original being deleted.
		dst := filepath.Join(a.configDir, "torrents", mi.HashInfoBytes().HexString()+".torrent")
		if !strings.EqualFold(src, dst) {
			if err := copyFile(src, dst); err != nil {
				return err
			}
		}
		src = dst
	}
	a.mu.Lock()
	dir := a.settings.DownloadDir
	a.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	hash, err := eng.Add(engine.AddOptions{Source: src, Dir: dir})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.saved {
		if s.Hash == hash {
			return nil
		}
	}
	a.saved = append(a.saved, savedTorrent{Hash: hash, Source: src, Dir: dir})
	return a.writeSaved()
}

func (a *App) Pause(hash string) error  { return a.setPaused(hash, true) }
func (a *App) Resume(hash string) error { return a.setPaused(hash, false) }

func (a *App) setPaused(hash string, paused bool) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	if paused {
		err = eng.Pause(hash)
	} else {
		err = eng.Resume(hash)
	}
	if err != nil {
		return err
	}
	a.updateSaved(hash, func(s *savedTorrent) { s.Paused = paused })
	return nil
}

func (a *App) PauseAll() {
	for _, t := range a.ListTorrents() {
		if t.State != "paused" {
			a.Pause(t.Hash)
		}
	}
}

func (a *App) ResumeAll() {
	for _, t := range a.ListTorrents() {
		if t.State == "paused" {
			a.Resume(t.Hash)
		}
	}
}

// Remove drops a torrent from the list, optionally deleting its data.
func (a *App) Remove(hash string, deleteFiles bool) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	rmErr := eng.Remove(hash, deleteFiles)
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.saved[:0]
	for _, s := range a.saved {
		if s.Hash == hash {
			if strings.HasPrefix(s.Source, filepath.Join(a.configDir, "torrents")) {
				os.Remove(s.Source)
			}
			continue
		}
		kept = append(kept, s)
	}
	a.saved = kept
	if err := a.writeSaved(); err != nil {
		return err
	}
	if rmErr != nil {
		return fmt.Errorf("removed, but some files couldn't be deleted: %w", rmErr)
	}
	return nil
}

func (a *App) GetFiles(hash string) ([]engine.FileStatus, error) {
	eng, err := a.engine()
	if err != nil {
		return nil, err
	}
	return eng.Files(hash)
}

func (a *App) SetFilesWanted(hash string, indexes []int, wanted bool) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	skipped, err := eng.SetFilesWanted(hash, indexes, wanted)
	if err != nil {
		return err
	}
	a.updateSaved(hash, func(s *savedTorrent) { s.Skipped = skipped })
	return nil
}

// OpenFolder opens a torrent's folder in Explorer, or the default download
// folder if hash is empty.
func (a *App) OpenFolder(hash string) error {
	a.mu.Lock()
	dir := a.settings.DownloadDir
	for _, s := range a.saved {
		if s.Hash == hash {
			dir = s.Dir
		}
	}
	a.mu.Unlock()
	// Show the torrent's own folder, or select its file if it's a single
	// file, once its data exists. Otherwise fall back to the download folder.
	if hash != "" {
		if eng, err := a.engine(); err == nil {
			if p, isDir, ok := eng.ContentPath(hash); ok {
				if _, err := os.Stat(p); err == nil {
					return platform.RevealPath(p, isDir)
				}
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return platform.OpenFolder(dir)
}

// --- Settings (bound to the frontend) ---

type SettingsView struct {
	Settings       Settings             `json:"settings"`
	CustomEndpoint string               `json:"customEndpoint"` // "" if no config imported
	Assoc          platform.AssocStatus `json:"assoc"`
	// Accounts maps provider ID to a display name for signed-in accounts.
	Accounts map[string]string `json:"accounts"`
	// Antivirus the app scans finished downloads with, and whether it's installed.
	Scanner          string `json:"scanner"`
	ScannerAvailable bool   `json:"scannerAvailable"`
}

func (a *App) GetSettings() SettingsView {
	a.mu.Lock()
	v := SettingsView{Settings: a.settings, Accounts: map[string]string{}}
	v.Scanner, v.ScannerAvailable = scan.Scanner()
	a.mu.Unlock()
	for id, acc := range a.loadAccounts() {
		v.Accounts[id] = acc.Display
	}
	if data, err := secret.ReadFile(a.customConfPath()); err == nil {
		if cfg, err := tunnel.ParseWGQuick(string(data)); err == nil {
			v.CustomEndpoint = cfg.Endpoint
		}
	}
	if !storeBuild { // the Store package registers these itself
		v.Assoc = platform.Associations(a.exe)
	}
	return v
}

// SaveSettings persists and applies settings. Speed limits and the download
// folder apply immediately; VPN changes trigger a reconnect.
func (a *App) SaveSettings(s Settings) error {
	if s.DownloadDir == "" {
		return errors.New("download folder can't be empty")
	}
	if s.VpnProvider != "custom" && s.VpnProvider != "account" && s.VpnProvider != "none" {
		s.VpnProvider = "warp"
	}
	if s.VpnProvider == "account" && a.account(s.AccountProvider) == nil {
		return errors.New("sign in to a VPN account before switching to it")
	}
	if s.ForwardedPort < 0 || s.ForwardedPort > 65535 {
		return errors.New("forwarded port must be between 0 and 65535")
	}
	if s.DownLimitKB < 0 || s.UpLimitKB < 0 {
		return errors.New("speed limits can't be negative")
	}
	if s.VpnProvider == "custom" {
		if _, err := os.Stat(a.customConfPath()); err != nil {
			return errors.New("import a WireGuard config before switching to it")
		}
	}

	a.mu.Lock()
	old := a.settings
	// The remote control has its own controls (SetRemote); the Settings
	// form's copy may be stale.
	s.RemoteEnabled, s.RemotePort, s.RemoteTailscale = old.RemoteEnabled, old.RemotePort, old.RemoteTailscale
	a.settings = s
	eng := a.eng
	a.mu.Unlock()
	if err := a.writeSettings(); err != nil {
		return err
	}
	if eng != nil {
		eng.SetLimits(s.DownLimitKB*1024, s.UpLimitKB*1024)
	}
	vpnChanged := old.VpnProvider != s.VpnProvider ||
		(s.VpnProvider == "custom" && old.ForwardedPort != s.ForwardedPort) ||
		(s.VpnProvider == "account" && (old.AccountProvider != s.AccountProvider || old.AccountRegion != s.AccountRegion))
	if vpnChanged {
		a.Reconnect()
	}
	return nil
}

// ChooseDirectory shows a folder picker and returns the choice ("" if cancelled).
func (a *App) ChooseDirectory(current string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Download folder",
		DefaultDirectory: existingAncestor(current),
	})
}

// existingAncestor returns dir, or its nearest parent that exists, or "" if
// none does. The OS folder picker fails if told to start in a missing folder
// (e.g. the user deleted the download folder).
func existingAncestor(dir string) string {
	for dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// ChooseDownloadDir picks and immediately saves a new default download folder.
func (a *App) ChooseDownloadDir() (string, error) {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	dir, err := a.ChooseDirectory(s.DownloadDir)
	if err != nil || dir == "" {
		return s.DownloadDir, err
	}
	s.DownloadDir = dir
	return dir, a.SaveSettings(s)
}

// ImportWireGuardConfig lets the user pick a .conf file, validates it and
// stores it. Returns the endpoint for display. If the custom provider is
// already active, it reconnects with the new config.
func (a *App) ImportWireGuardConfig() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Import WireGuard config",
		Filters: []runtime.FileFilter{{DisplayName: "WireGuard config (*.conf)", Pattern: "*.conf"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cfg, err := tunnel.ParseWGQuick(string(data))
	if err != nil {
		return "", err
	}
	// Stored encrypted; the original file the user picked is left alone.
	if err := secret.WriteFile(a.customConfPath(), data); err != nil {
		return "", err
	}
	a.mu.Lock()
	active := a.settings.VpnProvider == "custom"
	a.mu.Unlock()
	if active {
		a.Reconnect()
	}
	return cfg.Endpoint, nil
}

func (a *App) RegisterAssociations() (platform.AssocStatus, error) {
	if storeBuild {
		return platform.AssocStatus{}, errors.New("the Microsoft Store version registers magnet links and .torrent files when installed")
	}
	if err := platform.RegisterAssociations(a.exe); err != nil {
		return platform.AssocStatus{}, err
	}
	return platform.Associations(a.exe), nil
}

func (a *App) OpenDefaultAppsSettings() error {
	return platform.OpenDefaultAppsSettings()
}

// --- Persistence ---

func (a *App) customConfPath() string { return filepath.Join(a.configDir, "wireguard.conf") }

func (a *App) loadSettings() {
	home, _ := os.UserHomeDir()
	a.settings = Settings{
		DownloadDir:     filepath.Join(home, "Downloads", "Torrents"),
		VpnProvider:     "warp",
		CloseToTray:     true,
		AutoUpdateCheck: true,
		ScanDownloads:   true,
	}
	if data, err := os.ReadFile(filepath.Join(a.configDir, "settings.json")); err == nil {
		json.Unmarshal(data, &a.settings)
	}
}

func (a *App) writeSettings() error {
	a.mu.Lock()
	data, _ := json.MarshalIndent(a.settings, "", "  ")
	a.mu.Unlock()
	return writeFile(filepath.Join(a.configDir, "settings.json"), data)
}

func (a *App) loadSaved() {
	if data, err := os.ReadFile(filepath.Join(a.configDir, "torrents.json")); err == nil {
		json.Unmarshal(data, &a.saved)
	}
	for i := range a.saved {
		if a.saved[i].Dir == "" { // saved by an older version
			a.saved[i].Dir = a.settings.DownloadDir
		}
	}
}

func (a *App) updateSaved(hash string, f func(*savedTorrent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.saved {
		if a.saved[i].Hash == hash {
			f(&a.saved[i])
		}
	}
	if err := a.writeSaved(); err != nil {
		runtime.LogErrorf(a.ctx, "save torrents: %v", err)
	}
}

// writeSaved must be called with a.mu held.
func (a *App) writeSaved() error {
	data, _ := json.MarshalIndent(a.saved, "", "  ")
	return writeFile(filepath.Join(a.configDir, "torrents.json"), data)
}

// writeFile writes atomically so a crash can't leave a half-written file.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// GetPieceMap returns a torrent's pieces as runs of states for the piece map.
func (a *App) GetPieceMap(hash string) ([]engine.PieceRun, error) {
	eng, err := a.engine()
	if err != nil {
		return nil, err
	}
	return eng.PieceMap(hash)
}
