// Package engine wraps anacrolix/torrent so that every network operation it
// performs (peers, trackers, DHT, webseeds, DNS) goes through the tunnel.
// No OS sockets are opened by the torrent client itself.
package engine

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"github.com/anacrolix/torrent/metainfo"

	"torrent-client/internal/tunnel"
)

var defaultBootstrap = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.libtorrent.org:25401",
}

type Engine struct {
	cl  *torrent.Client
	dht *dht.Server
	tun *tunnel.Tunnel

	mu      sync.Mutex
	paused  map[metainfo.Hash]bool
	lastTot map[metainfo.Hash]rateSample
}

type rateSample struct {
	at        time.Time
	read      int64
	written   int64
	downRate  int64
	upRate    int64
}

// New starts a torrent client bound entirely to tun. dataDir is where
// downloads are saved.
func New(tun *tunnel.Tunnel, dataDir string) (*Engine, error) {
	tnet := tun.Net()

	// Force every DNS lookup in this process through the tunnel, regardless
	// of which nameserver the OS would use. Covers UDP tracker hostnames,
	// which anacrolix resolves with net.ResolveUDPAddr.
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return tun.DialContext(ctx, network, "1.1.1.1:53")
		},
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = true

	// No OS sockets: we add tunnel-backed dialers/DHT below instead.
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true // UPnP would map a port on the real LAN
	cfg.DisableWebtorrent = true       // WebRTC uses OS sockets
	cfg.AcceptPeerConnections = false  // free WARP has no inbound ports

	cfg.TrackerDialContext = tun.DialContext
	cfg.HTTPDialContext = tun.DialContext
	cfg.TrackerListenPacket = func(network, addr string) (net.PacketConn, error) {
		return tun.ListenUDP4()
	}
	cfg.DhtStartingNodes = func(string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) { return resolveBootstrap(tnet.LookupHost) }
	}

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	cl.AddDialer(dialer.WithNetwork{Network: "tcp", Dialer: tun})

	pc, err := tun.ListenUDP4()
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("dht socket: %w", err)
	}
	ds, err := cl.NewAnacrolixDhtServer(pc)
	if err != nil {
		pc.Close()
		cl.Close()
		return nil, fmt.Errorf("dht server: %w", err)
	}
	cl.AddDhtServer(torrent.AnacrolixDhtServerWrapper{Server: ds})

	return &Engine{
		cl:      cl,
		dht:     ds,
		tun:     tun,
		paused:  map[metainfo.Hash]bool{},
		lastTot: map[metainfo.Hash]rateSample{},
	}, nil
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

// Close stops the client. Call before closing the tunnel.
func (e *Engine) Close() {
	e.cl.Close()
	// Servers added via AddDhtServer aren't closed by the client, and the DHT
	// read loop panics if its socket dies while it's still open.
	e.dht.Close()
}

// Add accepts a magnet link or a path to a .torrent file.
func (e *Engine) Add(src string) (string, error) {
	var t *torrent.Torrent
	var err error
	if strings.HasPrefix(src, "magnet:") {
		t, err = e.cl.AddMagnet(src)
	} else {
		var mi *metainfo.MetaInfo
		mi, err = metainfo.LoadFromFile(src)
		if err == nil {
			t, err = e.cl.AddTorrent(mi)
		}
	}
	if err != nil {
		return "", err
	}
	go func() {
		<-t.GotInfo()
		e.mu.Lock()
		paused := e.paused[t.InfoHash()]
		e.mu.Unlock()
		if !paused {
			t.DownloadAll()
		}
	}()
	return t.InfoHash().HexString(), nil
}

func (e *Engine) find(hash string) (*torrent.Torrent, error) {
	var h metainfo.Hash
	if err := h.FromHexString(hash); err != nil {
		return nil, err
	}
	t, ok := e.cl.Torrent(h)
	if !ok {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	return t, nil
}

func (e *Engine) Pause(hash string) error {
	t, err := e.find(hash)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.paused[t.InfoHash()] = true
	e.mu.Unlock()
	t.DisallowDataDownload()
	t.DisallowDataUpload()
	return nil
}

func (e *Engine) Resume(hash string) error {
	t, err := e.find(hash)
	if err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.paused, t.InfoHash())
	e.mu.Unlock()
	t.AllowDataDownload()
	t.AllowDataUpload()
	if t.Info() != nil {
		t.DownloadAll()
	}
	return nil
}

// Remove drops the torrent from the client. Downloaded files are kept.
func (e *Engine) Remove(hash string) error {
	t, err := e.find(hash)
	if err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.paused, t.InfoHash())
	delete(e.lastTot, t.InfoHash())
	e.mu.Unlock()
	t.Drop()
	return nil
}

type Status struct {
	Hash      string  `json:"hash"`
	Name      string  `json:"name"`
	State     string  `json:"state"` // metadata, downloading, seeding, paused
	Size      int64   `json:"size"`
	Done      int64   `json:"done"`
	Progress  float64 `json:"progress"`
	DownRate  int64   `json:"downRate"`
	UpRate    int64   `json:"upRate"`
	Peers     int     `json:"peers"`
	Seeds     int     `json:"seeds"`
}

// List returns a snapshot of all torrents. Rates are computed from the
// change since the previous call.
func (e *Engine) List() []Status {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []Status
	for _, t := range e.cl.Torrents() {
		h := t.InfoHash()
		st := t.Stats()
		read := st.BytesReadUsefulData.Int64()
		written := st.BytesWrittenData.Int64()

		s := rateSample{at: now, read: read, written: written}
		if prev, ok := e.lastTot[h]; ok {
			if dt := now.Sub(prev.at).Seconds(); dt >= 0.5 {
				s.downRate = int64(float64(read-prev.read) / dt)
				s.upRate = int64(float64(written-prev.written) / dt)
			} else {
				s = prev // too soon; keep the previous sample and rates
			}
		}
		e.lastTot[h] = s

		row := Status{
			Hash:     h.HexString(),
			Name:     t.Name(),
			DownRate: s.downRate,
			UpRate:   s.upRate,
			Peers:    st.ActivePeers,
			Seeds:    st.ConnectedSeeders,
		}
		switch {
		case e.paused[h]:
			row.State = "paused"
		case t.Info() == nil:
			row.State = "metadata"
		case t.Complete().Bool():
			row.State = "seeding"
		default:
			row.State = "downloading"
		}
		if t.Info() != nil {
			row.Size = t.Length()
			row.Done = t.BytesCompleted()
			if row.Size > 0 {
				row.Progress = float64(row.Done) / float64(row.Size)
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
