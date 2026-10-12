package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"torrent-client/internal/engine"
	"torrent-client/internal/tunnel"
	"torrent-client/internal/warp"
)

type App struct {
	ctx       context.Context
	configDir string

	mu       sync.Mutex
	tun      *tunnel.Tunnel
	eng      *engine.Engine
	vpn      VpnStatus
	saved    []savedTorrent
	settings Settings
}

type VpnStatus struct {
	State    string `json:"state"` // connecting, connected, error
	ExitIP   string `json:"exitIP"`
	Location string `json:"location"`
	Colo     string `json:"colo"`
	Error    string `json:"error"`
}

type Settings struct {
	DownloadDir string `json:"downloadDir"`
}

// savedTorrent is persisted so torrents survive restarts. Source is a magnet
// link or a path to a .torrent copy inside the config dir.
type savedTorrent struct {
	Hash   string `json:"hash"`
	Source string `json:"source"`
	Paused bool   `json:"paused"`
}

func NewApp() *App {
	dir, _ := os.UserConfigDir()
	return &App{configDir: filepath.Join(dir, "torrent-client")}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadSettings()
	a.loadSaved()
	go a.connect()
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.eng != nil {
		a.eng.Close()
	}
	if a.tun != nil {
		a.tun.Close()
	}
}

func (a *App) setVpn(s VpnStatus) {
	a.mu.Lock()
	a.vpn = s
	a.mu.Unlock()
}

// connect registers WARP if needed, starts the tunnel, verifies it, then
// starts the torrent engine. Nothing is downloaded until this succeeds.
func (a *App) connect() {
	a.setVpn(VpnStatus{State: "connecting"})
	fail := func(err error) { a.setVpn(VpnStatus{State: "error", Error: err.Error()}) }

	acc, err := warp.LoadOrRegister(filepath.Join(a.configDir, "warp.json"))
	if err != nil {
		fail(err)
		return
	}
	tun, err := tunnel.Start(tunnel.Config{
		PrivateKey:    acc.PrivateKey,
		PeerPublicKey: acc.PeerPublicKey,
		Endpoint:      acc.Endpoint,
		Addresses:     []string{acc.AddressV4, acc.AddressV6},
		DNS:           []string{"1.1.1.1", "1.0.0.1"},
	})
	if err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tr, err := tun.CheckTrace(ctx)
	cancel()
	if err != nil {
		tun.Close()
		fail(fmt.Errorf("tunnel check failed: %w", err))
		return
	}
	if tr.Warp != "on" && tr.Warp != "plus" {
		tun.Close()
		fail(fmt.Errorf("tunnel is up but Cloudflare reports warp=%q", tr.Warp))
		return
	}

	a.mu.Lock()
	dataDir := a.settings.DownloadDir
	a.mu.Unlock()
	eng, err := engine.New(tun, dataDir)
	if err != nil {
		tun.Close()
		fail(err)
		return
	}

	a.mu.Lock()
	a.tun, a.eng = tun, eng
	a.vpn = VpnStatus{State: "connected", ExitIP: tr.IP, Location: tr.Location, Colo: tr.Colo}
	saved := append([]savedTorrent(nil), a.saved...)
	a.mu.Unlock()

	for _, s := range saved {
		if _, err := eng.Add(s.Source); err != nil {
			runtime.LogWarningf(a.ctx, "restore %s: %v", s.Hash, err)
			continue
		}
		if s.Paused {
			eng.Pause(s.Hash)
		}
	}
	go a.monitor()
}

// monitor re-checks the exit IP every minute so the UI reflects reality.
func (a *App) monitor() {
	for range time.Tick(60 * time.Second) {
		a.mu.Lock()
		tun := a.tun
		a.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		tr, err := tun.CheckTrace(ctx)
		cancel()
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

// --- Bound to the frontend ---

func (a *App) GetVpnStatus() VpnStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vpn
}

func (a *App) Retry() {
	a.mu.Lock()
	busy := a.eng != nil || a.vpn.State == "connecting"
	a.mu.Unlock()
	if !busy {
		go a.connect()
	}
}

func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *App) ListTorrents() []engine.Status {
	eng, err := a.engine()
	if err != nil {
		return []engine.Status{}
	}
	return eng.List()
}

func (a *App) AddMagnet(link string) error {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "magnet:") {
		return errors.New("not a magnet link")
	}
	return a.add(link)
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
		mi, err := metainfo.LoadFromFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		// Keep our own copy so the torrent survives the original being deleted.
		dst := filepath.Join(a.configDir, "torrents", mi.HashInfoBytes().HexString()+".torrent")
		if err := copyFile(p, dst); err != nil {
			return err
		}
		if err := a.add(dst); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) add(src string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	hash, err := eng.Add(src)
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
	a.saved = append(a.saved, savedTorrent{Hash: hash, Source: src})
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
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.saved {
		if a.saved[i].Hash == hash {
			a.saved[i].Paused = paused
		}
	}
	return a.writeSaved()
}

// Remove drops a torrent from the list. Downloaded files are kept.
func (a *App) Remove(hash string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	if err := eng.Remove(hash); err != nil {
		return err
	}
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
	return a.writeSaved()
}

func (a *App) OpenDownloadDir() error {
	a.mu.Lock()
	dir := a.settings.DownloadDir
	a.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return exec.Command("explorer", dir).Start()
}

// ChooseDownloadDir changes where new data is saved. Takes effect on restart,
// since the engine's storage is fixed when it starts.
func (a *App) ChooseDownloadDir() (string, error) {
	a.mu.Lock()
	cur := a.settings.DownloadDir
	a.mu.Unlock()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Download folder",
		DefaultDirectory: cur,
	})
	if err != nil || dir == "" {
		return cur, err
	}
	a.mu.Lock()
	a.settings.DownloadDir = dir
	a.mu.Unlock()
	return dir, a.writeSettings()
}

// --- Persistence ---

func (a *App) loadSettings() {
	home, _ := os.UserHomeDir()
	a.settings = Settings{DownloadDir: filepath.Join(home, "Downloads", "Torrents")}
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
}

// writeSaved must be called with a.mu held.
func (a *App) writeSaved() error {
	data, _ := json.MarshalIndent(a.saved, "", "  ")
	return writeFile(filepath.Join(a.configDir, "torrents.json"), data)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
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
