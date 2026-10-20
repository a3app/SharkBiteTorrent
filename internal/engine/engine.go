// Package engine wraps anacrolix/torrent so that every network operation it
// performs (peers, trackers, DHT, DNS) goes through the tunnel.
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
	"sync/atomic"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"sharkbite/internal/scan"
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
	// StateDir holds the piece-completion database (the app data folder).
	// Empty keeps completion in memory only (tests and tools).
	StateDir string
}

type Engine struct {
	cl       *torrent.Client
	dht      *dht.Server
	listener net.Listener
	tun      *tunnel.Tunnel
	down, up *rate.Limiter
	pc       storage.PieceCompletion // shared by all torrents; persists progress

	mu      sync.Mutex
	entries map[metainfo.Hash]*entry
}

// entry is our per-torrent state on top of anacrolix's.
type entry struct {
	t         *torrent.Torrent
	dir       string
	storage   storage.ClientImpl
	paused    bool
	skipped   map[int]bool // file indexes the user doesn't want
	added     time.Time
	warnings  []scan.Warning // from the file list, computed once metadata arrives
	inspected bool
	checking  atomic.Bool // verifying existing data after a restart

	// "Verify files": re-hash finished pieces on disk (see Verify).
	verifying            atomic.Bool
	verifyDone, verifyOf atomic.Int64
	verified             *VerifyResult // last result; guarded by Engine.mu

	last               time.Time
	lastRead, lastWrit int64
	downRate, upRate   int64
}

// systemResolver is the resolver the process started with, restored when the
// engine runs without the VPN (New points the default at the tunnel).
var systemResolver = net.DefaultResolver

// New starts a torrent client bound entirely to tun. With a nil tun (the
// user chose "No VPN") it uses the computer's normal network instead; see
// newDirect. The VPN path below never falls back to OS sockets.
func New(tun *tunnel.Tunnel, opts Options) (*Engine, error) {
	if tun == nil {
		return newDirect(opts)
	}
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
	if opts.StateDir != "" {
		pc, err := storage.NewBoltPieceCompletion(opts.StateDir)
		if err != nil {
			return nil, fmt.Errorf("opening download progress database: %w", err)
		}
		e.pc = pc
	} else {
		e.pc = storage.NewMapPieceCompletion()
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = true
	// No web seeds (HTTP mirrors listed in a torrent). anacrolix/torrent
	// v1.61.0 crashes the whole app when a torrent with active web seed
	// requests is removed: the requests outlive the torrent and a timer's
	// consistency check panics (see the comment in its Torrent.close).
	// Peers still serve everything; mirrors often refuse ranged requests.
	cfg.DisableWebseeds = true

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
	// Check (hash) and write finished pieces in parallel. Each check now also
	// writes the piece to disk in one go, which on a NAS means network
	// latency; with the default 2 hashers, checking fell far behind the
	// download and unchecked pieces piled up in memory.
	cfg.PieceHashersPerTorrent = 16
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

// newDirect starts the client on the computer's own network, for users who
// chose "No VPN" (for example because their router or system already runs
// one). It uses the library's normal sockets: TCP, DHT and UDP trackers, plus
// UPnP/NAT-PMP so peers can connect in.
func newDirect(opts Options) (*Engine, error) {
	net.DefaultResolver = systemResolver

	e := &Engine{
		down:    rate.NewLimiter(unlimited, 0),
		up:      rate.NewLimiter(unlimited, 0),
		entries: map[metainfo.Hash]*entry{},
	}
	e.SetLimits(opts.DownLimit, opts.UpLimit)
	if opts.StateDir != "" {
		pc, err := storage.NewBoltPieceCompletion(opts.StateDir)
		if err != nil {
			return nil, fmt.Errorf("opening download progress database: %w", err)
		}
		e.pc = pc
	} else {
		e.pc = storage.NewMapPieceCompletion()
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = true
	cfg.DisableWebseeds = true // see New
	cfg.EstablishedConnsPerTorrent = 200
	cfg.PieceHashersPerTorrent = 16 // see New
	cfg.DataDir = os.TempDir()
	cfg.DownloadRateLimiter = e.down
	cfg.UploadRateLimiter = e.up
	cfg.DisableUTP = true // as in VPN mode: no uTP (see README)
	cfg.DisableWebtorrent = true
	cfg.ListenPort = 0 // a free port; UPnP maps whichever we get

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		e.pc.Close()
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	e.cl = cl
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
	// After the client: dropping torrents flushes their last completion writes.
	e.pc.Close()
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
	// Recheck verifies data already on disk before downloading. Set it when
	// restoring a torrent after a restart: the storage forgets the progress of
	// unfinished (.part) files, and verifying recovers it.
	Recheck bool
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
	st := newFileStore(o.Dir, e.pc)
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
			if o.Recheck {
				recheck(en)
			}
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
	info := en.t.Info()
	en.t.Drop() // closes the storage, releasing its file handles
	if info != nil {
		// Forget its progress, so re-adding it later starts clean.
		for i := range info.NumPieces() {
			e.pc.Set(metainfo.PieceKey{InfoHash: en.t.InfoHash(), Index: i}, false)
		}
	}

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
	State    string  `json:"state"` // metadata, needs-torrent-file, checking, downloading, seeding, paused
	Size     int64   `json:"size"`  // bytes of wanted files
	Done     int64   `json:"done"`  // completed bytes of wanted files
	Progress float64 `json:"progress"`
	DownRate int64   `json:"downRate"`
	UpRate   int64   `json:"upRate"`
	Peers    int     `json:"peers"`
	Seeds    int     `json:"seeds"`
	// Warnings from the file list (see scan.Inspect), known before downloading.
	Warnings []scan.Warning `json:"warnings"`
	// Antivirus scan of the finished files; filled in by the app.
	ScanState  string   `json:"scanState"` // "", scanning, clean, threat, error, toolarge
	ScanEngine string   `json:"scanEngine"`
	Threats    []string `json:"threats"`
	ScanError  string   `json:"scanError"`
	// toolarge: files the antivirus skipped for their size; CanScanFully if
	// App.ScanFully would check at least one of them.
	TooLarge     []string `json:"tooLarge"`
	CanScanFully bool     `json:"canScanFully"`
	ScanSeen     bool     `json:"scanSeen"` // the user has opened the files panel since
	// "Verify files" (see Engine.Verify): progress while running, then the
	// last result. Bad pieces are downloaded again automatically.
	Verifying      bool          `json:"verifying"`
	VerifyProgress float64       `json:"verifyProgress"`
	Verified       *VerifyResult `json:"verified"`
	// .sfv check of the finished files; filled in by the app.
	SFV *SFVStatus `json:"sfv"`
}

// VerifyResult is the outcome of re-checking a torrent's data on disk.
type VerifyResult struct {
	Pieces   int      `json:"pieces"`   // pieces that were complete and got re-checked
	Bad      int      `json:"bad"`      // of those, how many no longer matched
	BadFiles []string `json:"badFiles"` // files containing bad pieces
	At       int64    `json:"at"`       // unix seconds
}

// SFVStatus is the result of checking the torrent's own .sfv file(s).
type SFVStatus struct {
	Checked  int      `json:"checked"`
	Bad      []string `json:"bad"`
	Missing  []string `json:"missing"`
	Error    string   `json:"error"`
	Checking bool     `json:"checking"`
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
		if t.Info() != nil && !en.inspected {
			var fs []scan.File
			for _, f := range t.Files() {
				fs = append(fs, scan.File{Path: f.DisplayPath(), Size: f.Length()})
			}
			en.warnings, en.inspected = scan.Inspect(fs), true
		}
		row.Warnings = en.warnings
		row.Verified = en.verified
		if en.verifying.Load() {
			row.Verifying = true
			if of := en.verifyOf.Load(); of > 0 {
				row.VerifyProgress = float64(en.verifyDone.Load()) / float64(of)
			}
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
		case en.checking.Load():
			row.State = "checking"
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

// recheck verifies the pieces of every unfinished file, so progress saved in
// .part files survives a restart. The storage marks those pieces incomplete
// on open (anacrolix's part-file mode only trusts finished files), so without
// this they would be downloaded again. It runs before priorities are applied,
// so nothing is re-requested while checking. Finished files are skipped.
func recheck(en *entry) {
	t := en.t
	var pieces []int
	for _, f := range t.Files() {
		if f.BytesCompleted() < f.Length() {
			for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
				pieces = append(pieces, i)
			}
		}
	}
	if len(pieces) == 0 {
		return
	}
	en.checking.Store(true)
	defer en.checking.Store(false)

	// VerifyData queues the piece on the library's hashers and waits; run a
	// bounded number at once so the queue stays full.
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, i := range pieces {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			t.Piece(i).VerifyData()
		}(i)
	}
	wg.Wait()
}

// Verify re-reads every finished piece from disk and checks it against the
// torrent's piece hashes (SHA-1, or SHA-256 for v2 torrents). That catches
// damage after the download: a bad copy, a failing disk or NAS. Pieces that
// no longer match are marked missing, so the torrent downloads them again.
// It blocks until done; progress shows in List.
func (e *Engine) Verify(ctx context.Context, hash string) (*VerifyResult, error) {
	en, err := e.find(hash)
	if err != nil {
		return nil, err
	}
	t := en.t
	if t.Info() == nil {
		return nil, errors.New("the torrent's file list isn't known yet")
	}
	if !en.verifying.CompareAndSwap(false, true) {
		return nil, errors.New("already verifying")
	}
	defer en.verifying.Store(false)

	var pieces []int
	for i := 0; i < t.NumPieces(); i++ {
		if t.PieceState(i).Complete {
			pieces = append(pieces, i)
		}
	}
	en.verifyDone.Store(0)
	en.verifyOf.Store(int64(len(pieces)))

	var mu sync.Mutex
	var bad []int
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, i := range pieces {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Add(1)
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			err := t.Piece(i).VerifyDataContext(ctx)
			if err == nil && !t.PieceState(i).Complete {
				mu.Lock()
				bad = append(bad, i)
				mu.Unlock()
			}
			en.verifyDone.Add(1)
		}(i)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	res := &VerifyResult{Pieces: len(pieces), Bad: len(bad), BadFiles: []string{}, At: time.Now().Unix()}
	for _, f := range t.Files() {
		for _, p := range bad {
			if p >= f.BeginPieceIndex() && p < f.EndPieceIndex() {
				res.BadFiles = append(res.BadFiles, f.DisplayPath())
				break
			}
		}
	}
	e.mu.Lock()
	en.verified = res
	e.mu.Unlock()
	return res, nil
}

// FilePaths returns where each finished, wanted file is on disk, keyed by
// its path inside the torrent (forward slashes).
func (e *Engine) FilePaths(hash string) (map[string]string, error) {
	en, err := e.find(hash)
	if err != nil {
		return nil, err
	}
	info := en.t.Info()
	if info == nil {
		return nil, errors.New("the torrent's file list isn't known yet")
	}
	root := filepath.Join(en.dir, info.BestName())
	out := map[string]string{}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, f := range en.t.Files() {
		if en.skipped[i] || f.BytesCompleted() < f.Length() {
			continue
		}
		rel := f.DisplayPath()
		if info.IsDir() {
			out[rel] = filepath.Join(root, filepath.FromSlash(rel))
		} else {
			out[rel] = root
		}
	}
	return out, nil
}

// ContentPath returns where a torrent's data lives: its folder for
// multi-file torrents, or its file for single-file ones. ok is false until
// the torrent's metadata is known.
func (e *Engine) ContentPath(hash string) (path string, isDir bool, ok bool) {
	en, err := e.find(hash)
	if err != nil {
		return "", false, false
	}
	info := en.t.Info()
	if info == nil {
		return en.dir, true, false
	}
	return filepath.Join(en.dir, info.BestName()), info.IsDir(), true
}

// PieceRun is a run of consecutive pieces in the same state, for the
// piece map. State is one of: done, checking, partial (downloading),
// missing, skipped (in files the user unticked).
type PieceRun struct {
	State string `json:"s"`
	Count int    `json:"n"`
}

// PieceMap returns the torrent's pieces as runs of states, in order. Empty
// until metadata is known.
func (e *Engine) PieceMap(hash string) ([]PieceRun, error) {
	en, err := e.find(hash)
	if err != nil {
		return nil, err
	}
	if en.t.Info() == nil {
		return []PieceRun{}, nil
	}
	var out []PieceRun
	for _, r := range en.t.PieceStateRuns() {
		s := "missing"
		switch {
		case r.Complete:
			s = "done"
		case r.Hashing || r.QueuedForHash || r.Marking || r.Checking:
			s = "checking"
		case r.Partial:
			s = "partial"
		case r.Priority == torrent.PiecePriorityNone:
			s = "skipped"
		}
		// Merge with the previous run if only fields we don't show differ.
		if n := len(out); n > 0 && out[n-1].State == s {
			out[n-1].Count += r.Length
		} else {
			out = append(out, PieceRun{State: s, Count: r.Length})
		}
	}
	return out, nil
}
