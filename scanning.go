package main

import (
	"context"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sharkbite/internal/engine"
	"sharkbite/internal/scan"
)

// Malware checks on finished torrents. The file-list check (scan.Inspect)
// runs in the engine as soon as metadata arrives; this file handles what
// happens once a torrent has finished downloading.

type scanRecord struct {
	Engine  string   `json:"engine"`
	Clean   bool     `json:"clean"`
	Threats []string `json:"threats,omitempty"`
	Error   string   `json:"error,omitempty"`
	At      int64    `json:"at"` // unix seconds
}

// scanLoop looks for newly finished torrents and checks them, one at a time,
// until ctx ends (the VPN disconnects or the app quits).
func (a *App) scanLoop(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		eng, err := a.engine()
		if err != nil {
			continue
		}
		for _, t := range eng.List() {
			if t.State != "seeding" || ctx.Err() != nil {
				continue
			}
			a.mu.Lock()
			s := a.savedFor(t.Hash)
			busy := a.scanning[t.Hash]
			a.mu.Unlock()
			if s == nil || s.Scan != nil || busy {
				continue
			}
			a.checkFinished(ctx, eng, t)
		}
	}
}

// savedFor returns the saved entry for hash. Call with a.mu held.
func (a *App) savedFor(hash string) *savedTorrent {
	for i := range a.saved {
		if a.saved[i].Hash == hash {
			return &a.saved[i]
		}
	}
	return nil
}

// checkFinished marks a finished torrent's files as downloaded from the
// internet and, if enabled and available, scans them with the antivirus.
func (a *App) checkFinished(ctx context.Context, eng *engine.Engine, t engine.Status) {
	p, _, ok := eng.ContentPath(t.Hash)
	if !ok {
		return
	}
	scan.MarkDownloaded(p)

	a.mu.Lock()
	enabled := a.settings.ScanDownloads
	a.mu.Unlock()
	name, available := scan.Scanner()
	rec := &scanRecord{Engine: name, At: time.Now().Unix()}
	switch {
	case !enabled:
		rec.Error = "scanning is turned off in Settings"
	case !available:
		rec.Error = name + " isn't installed"
	default:
		a.mu.Lock()
		if a.scanning == nil {
			a.scanning = map[string]bool{}
		}
		a.scanning[t.Hash] = true
		a.mu.Unlock()

		sctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		res, err := scan.ScanPath(sctx, p)
		cancel()

		a.mu.Lock()
		delete(a.scanning, t.Hash)
		a.mu.Unlock()
		if ctx.Err() != nil {
			return // app quitting or VPN reconnecting: try again next time
		}
		if err != nil {
			rec.Error = err.Error()
		} else {
			rec.Clean, rec.Threats = res.Clean, res.Threats
		}
	}

	a.mu.Lock()
	if s := a.savedFor(t.Hash); s != nil {
		s.Scan = rec
		a.writeSaved()
	}
	a.mu.Unlock()

	if len(rec.Threats) > 0 {
		runtime.EventsEmit(a.ctx, "error", fmt.Sprintf(
			"⚠ %s found a threat in “%s”: %v. Don't open its files; consider removing it with its files.",
			rec.Engine, t.Name, rec.Threats))
	}
}

// withScans adds scan results to the torrent list for the UI.
func (a *App) withScans(list []engine.Status) []engine.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range list {
		t := &list[i]
		if a.scanning[t.Hash] {
			t.ScanState = "scanning"
			continue
		}
		s := a.savedFor(t.Hash)
		if s == nil || s.Scan == nil {
			continue
		}
		t.ScanEngine = s.Scan.Engine
		switch {
		case len(s.Scan.Threats) > 0:
			t.ScanState, t.Threats = "threat", s.Scan.Threats
		case s.Scan.Error != "":
			t.ScanState, t.ScanError = "error", s.Scan.Error
		case s.Scan.Clean:
			t.ScanState = "clean"
		}
	}
	return list
}
