// Package engine wraps anacrolix/torrent so that every network operation it
// performs (peers, trackers, DHT, webseeds, DNS) goes through the tunnel.
// No OS sockets are opened by the torrent client itself.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"sharkbite/internal/tunnel"
)

var defaultBootstrap = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.libtorrent.org:25401",
}

type Options struct {
	// ForwardedPort, if non-zero, is a port the VPN provider forwards to us.
	// The engine accepts inbound peers on it and announces it to trackers/DHT.
	ForwardedPort int
	// Speed limits in bytes/second; 0 means unlimited.
	DownLimit int64
	UpLimit   int64
}

type Engine struct {
	cl       *torrent.Client
	dht      *dht.Server
	listener net.Listener
	tun      *tunnel.Tunnel
	down, up *rate.Limiter

	mu      sync.Mutex
	entries map[metainfo.Hash]*entry
}

// entry is our per-torrent state on top of anacrolix's.
type entry struct {
	t       *torrent.Torrent
	dir     string
	storage storage.ClientImplCloser
	paused  bool
	skipped map[int]bool // file indexes the user doesn't want
	added   time.Time

	last               time.Time
	lastRead, lastWrit int64
	downRate, upRate   int64
}

// New starts a torrent client bound entirely to tun.
func New(tun *tunnel.Tunnel, opts Options) (*Engine, error) {
	tnet := tun.Net()

	// Force every DNS lookup in this process through the tunnel, regardless
	// of which nameserver the OS would use. Covers UDP tracker hostnames,
	// which anacrolix resolves with net.ResolveUDPAddr.
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return tun.DialContext(ctx, network, tun.DNSServer())
		},
	}

	e := &Engine{
		tun:     tun,
		down:    rate.NewLimiter(unlimited, 0),
		up:      rate.NewLimiter(unlimited, 0),
		entries: map[metainfo.Hash]*entry{},
	}
	e.SetLimits(opts.DownLimit, opts.UpLimit)

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = true

	// Connect to more peers, faster. The library's defaults assume OS sockets
	// (where Windows limits half-open connections) and wait 20s per dial. Our
	// dials go through the userspace tunnel, which has no such limits, and
	// behind a VPN without inbound connections many swarm members are
	// firewalled and never answer, so short timeouts and more parallel
	// attempts reach the connectable peers much sooner.
	cfg.NominalDialTimeout = 8 * time.Second
	// Effectively unlimited for real swarms (most peers behind a VPN are
	// unreachable anyway); still bounded so one huge swarm can't exhaust memory.
	// Bandwidth is controlled with the speed limits, not the peer count.
	cfg.EstablishedConnsPerTorrent = 200
	cfg.HalfOpenConnsPerTorrent = 60
	cfg.TotalHalfOpenConns = 250
	cfg.DialRateLimiter = rate.NewLimiter(40, 40)
	// Torrents get their own storage (see Add); this only catches anything
	// added without one, and is never written to in practice.
	cfg.DataDir = os.TempDir()
	cfg.DownloadRateLimiter = e.down
	cfg.UploadRateLimiter = e.up

	// No OS sockets: we add tunnel-backed dialers/listeners/DHT below instead.
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true // UPnP would map a port on the real LAN
	cfg.DisableWebtorrent = true       // WebRTC uses OS sockets
	cfg.AcceptPeerConnections = opts.ForwardedPort > 0

	cfg.TrackerDialContext = tun.DialContext
	cfg.HTTPDialContext = tun.DialContext
	cfg.TrackerListenPacket = func(network, addr string) (net.PacketConn, error) {
		return tun.ListenUDP(network)
	}
	cfg.DhtStartingNodes = func(string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) { return resolveBootstrap(tnet.LookupHost) }
	}

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	e.cl = cl
	cl.AddDialer(dialer.WithNetwork{Network: "tcp", Dialer: tun})

	if opts.ForwardedPort > 0 {
		l, err := tun.ListenTCP4(opts.ForwardedPort)
		if err != nil {
			cl.Close()
			return nil, fmt.Errorf("listen on forwarded port %d: %w", opts.ForwardedPort, err)
		}
		e.listener = l
		cl.AddListener(l) // also makes the client announce this port
	}

	pc, err := tun.ListenUDP4()
	if err != nil {
		e.closeClient()
		return nil, fmt.Errorf("dht socket: %w", err)
	}
	ds, err := cl.NewAnacrolixDhtServer(pc)
	if err != nil {
		pc.Close()
		e.closeClient()
		return nil, fmt.Errorf("dht server: %w", err)
	}
	e.dht = ds
	cl.AddDhtServer(torrent.AnacrolixDhtServerWrapper{Server: ds})
	return e, nil
}

func resolveBootstrap(lookup func(string) ([]string, error)) ([]dht.Addr, error) {
	var addrs []dht.Addr
	for _, hp := range defaultBootstrap {
		host, port, _ := net.SplitHostPort(hp)
		ips, err := lookup(host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			ua, err := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, port))
			if err == nil {
				addrs = append(addrs, dht.NewAddr(ua))
			}
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no DHT bootstrap nodes resolved")
	}
	return addrs, nil
}

func (e *Engine) closeClient() {
	e.cl.Close()
	if e.listener != nil {
		e.listener.Close()
	}
	e.mu.Lock()
	for _, en := range e.entries {
		en.storage.Close()
	}
	e.mu.Unlock()
}

// Close stops the client. Call before closing the tunnel.
func (e *Engine) Close() {
	e.closeClient()
	// Servers added via AddDhtServer aren't closed by the client, and the DHT
	// read loop panics if its socket dies while it's still open.
	if e.dht != nil {
		e.dht.Close()
	}
	// Stop routing this process's DNS into a tunnel that's going away.
	net.DefaultResolver = &net.Resolver{}
}

// SetLimits changes the global speed limits (bytes/s, 0 = unlimited) live.
func (e *Engine) SetLimits(down, up int64) {
	e.down.SetLimit(limitOf(down))
	e.up.SetLimit(limitOf(up))
}

// unlimited is used instead of rate.Inf: anacrolix stops opening peer
// connections when the download limiter reports no tokens, and an Inf limiter
// never refills its token count from zero. A huge finite rate refills instantly.
const unlimited = rate.Limit(1 << 40)

func limitOf(n int64) rate.Limit {
	if n <= 0 {
		return unlimited
	}
	return rate.Limit(n)
}

type AddOptions struct {
	Source  string // magnet link or path to a .torrent file
	Dir     string // folder to save data in
	Skipped []int  // file indexes not to download
	Paused  bool
}

// Add starts a torrent and returns its info hash.
func (e *Engine) Add(o AddOptions) (string, error) {
	var spec *torrent.TorrentSpec
	var err error
	if strings.HasPrefix(o.Source, "magnet:") {
		spec, err = torrent.TorrentSpecFromMagnetUri(o.Source)
	} else {
		var mi *metainfo.MetaInfo
		if mi, err = metainfo.LoadFromFile(o.Source); err == nil {
			spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
		}
	}
	if err != nil {
		return "", err
	}

	e.mu.Lock()
	if _, ok := e.entries[spec.InfoHash]; ok {
		e.mu.Unlock()
		return spec.InfoHash.HexString(), nil
	}
	// Part files: incomplete files end in .part, and completion is inferred
	// from the files on disk, so no database is needed per folder.
	st := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: o.Dir})
	spec.Storage = st
	en := &entry{dir: o.Dir, storage: st, paused: o.Paused, skipped: map[int]bool{}, added: time.Now()}
	for _, i := range o.Skipped {
		en.skipped[i] = true
	}
	e.entries[spec.InfoHash] = en
	e.mu.Unlock()

	t, _, err := e.cl.AddTorrentSpec(spec)
	if err != nil {
		e.mu.Lock()
		delete(e.entries, spec.InfoHash)
		e.mu.Unlock()
		st.Close()
		return "", err
	}
	en.t = t
	if o.Paused {
		t.DisallowDataDownload()
		t.DisallowDataUpload()
	}
	go func() {
		select {
		case <-t.GotInfo():
			e.applyPriorities(en)
		case <-t.Closed():
		}
	}()
	return t.InfoHash().HexString(), nil
}

// applyPriorities marks wanted files Normal and skipped ones None.
func (e *Engine) applyPriorities(en *entry) {
	e.mu.Lock()
	skipped := make(map[int]bool, len(en.skipped))
	for k, v := range en.skipped {
		skipped[k] = v
	}
	e.mu.Unlock()
	for i, f := range en.t.Files() {
		if skipped[i] {
			f.SetPriority(torrent.PiecePriorityNone)
		} else {
			f.SetPriority(torrent.PiecePriorityNormal)
		}
	}
}

func (e *Engine) find(hash string) (*entry, error) {
	var h metainfo.Hash
	if err := h.FromHexString(hash); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.entries[h]
	if !ok || en.t == nil {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	return en, nil
}

func (e *Engine) Pause(hash string) error {
	en, err := e.find(hash)
	if err != nil {
		return err
	}
	e.mu.Lock()
	en.paused = true
	e.mu.Unlock()
	en.t.DisallowDataDownload()
	en.t.DisallowDataUpload()
	return nil
}

func (e *Engine) Resume(hash string) error {
	en, err := e.find(hash)
	if err != nil {
		return err
	}
	e.mu.Lock()
	en.paused = false
	e.mu.Unlock()
	en.t.AllowDataDownload()
	en.t.AllowDataUpload()
	return nil
}

// Remove drops the torrent. If deleteFiles is set, its downloaded data
// (including .part files) is deleted too.
func (e *Engine) Remove(hash string, deleteFiles bool) error {
	en, err := e.find(hash)
	if err != nil {
		return err
	}
	var paths []string
	if info := en.t.Info(); info != nil && deleteFiles {
		name := info.BestName()
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("refusing to delete files for torrent with unsafe name %q", name)
		}
		p := filepath.Join(en.dir, name)
		paths = []string{p, p + ".part"}
	}
	e.mu.Lock()
	delete(e.entries, en.t.InfoHash())
	e.mu.Unlock()
	en.t.Drop()
	en.storage.Close()

	var errs []error
	for _, p := range paths {
		// Windows can hold file handles briefly after Drop; retry a little.
		var err error
		for range 10 {
			if err = os.RemoveAll(p); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type FileStatus struct {
	Index  int    `json:"index"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Done   int64  `json:"done"`
	Wanted bool   `json:"wanted"`
}

// Files lists a torrent's files; empty until metadata is available.
func (e *Engine) Files(hash string) ([]FileStatus, error) {
	en, err := e.find(hash)
	if err != nil {
		return nil, err
	}
	if en.t.Info() == nil {
		return []FileStatus{}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	files := en.t.Files()
	out := make([]FileStatus, len(files))
	for i, f := range files {
		out[i] = FileStatus{
			Index:  i,
			Path:   f.DisplayPath(),
			Size:   f.Length(),
			Done:   f.BytesCompleted(),
			Wanted: !en.skipped[i],
		}
	}
	return out, nil
}

// SetFilesWanted changes which files are downloaded and returns the full
// list of skipped indexes (for persisting).
func (e *Engine) SetFilesWanted(hash string, indexes []int, wanted bool) ([]int, error) {
	en, err := e.find(hash)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	for _, i := range indexes {
		if wanted {
			delete(en.skipped, i)
		} else {
			en.skipped[i] = true
		}
	}
	skipped := make([]int, 0, len(en.skipped))
	for i := range en.skipped {
		skipped = append(skipped, i)
	}
	e.mu.Unlock()
	sort.Ints(skipped)
	if en.t.Info() != nil {
		e.applyPriorities(en)
	}
	return skipped, nil
}

type Status struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	Dir      string  `json:"dir"`
	State    string  `json:"state"` // metadata, needs-torrent-file, downloading, seeding, paused
	Size     int64   `json:"size"`  // bytes of wanted files
	Done     int64   `json:"done"`  // completed bytes of wanted files
	Progress float64 `json:"progress"`
	DownRate int64   `json:"downRate"`
	UpRate   int64   `json:"upRate"`
	Peers    int     `json:"peers"`
	Seeds    int     `json:"seeds"`
}

// List returns a snapshot of all torrents. Rates are computed from the
// change since the previous call.
func (e *Engine) List() []Status {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	out := []Status{}
	for h, en := range e.entries {
		t := en.t
		if t == nil {
			continue
		}
		st := t.Stats()
		read := st.BytesReadUsefulData.Int64()
		written := st.BytesWrittenData.Int64()
		if dt := now.Sub(en.last).Seconds(); dt >= 0.5 {
			if !en.last.IsZero() {
				en.downRate = int64(math.Max(0, float64(read-en.lastRead)/dt))
				en.upRate = int64(math.Max(0, float64(written-en.lastWrit)/dt))
			}
			en.last, en.lastRead, en.lastWrit = now, read, written
		}

		row := Status{
			Hash:     h.HexString(),
			Name:     t.Name(),
			Dir:      en.dir,
			DownRate: en.downRate,
			UpRate:   en.upRate,
			Peers:    st.ActivePeers,
			Seeds:    st.ConnectedSeeders,
		}
		if t.Info() != nil {
			for i, f := range t.Files() {
				if en.skipped[i] {
					continue
				}
				row.Size += f.Length()
				row.Done += f.BytesCompleted()
			}
			if row.Size > 0 {
				row.Progress = float64(row.Done) / float64(row.Size)
			} else {
				row.Progress = 1
			}
		}
		switch {
		case en.paused:
			row.State = "paused"
		case t.Info() == nil && st.ActivePeers > 0 && now.Sub(en.added) > metadataStallAfter:
			// Connected, but nobody will send the file list: almost always a
			// private torrent, whose peers disable metadata exchange (ut_metadata).
			row.State = "needs-torrent-file"
		case t.Info() == nil:
			row.State = "metadata"
		case row.Done >= row.Size:
			row.State = "seeding"
		default:
			row.State = "downloading"
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WriteStatus dumps the client's internal state (trackers, DHT, peers) for debugging.
func (e *Engine) WriteStatus(w io.Writer) { e.cl.WriteStatus(w) }

// metadataStallAfter is how long a magnet may sit connected to peers without
// receiving metadata before the UI suggests using the .torrent file instead.
const metadataStallAfter = 45 * time.Second
