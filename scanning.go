package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sharkbite/internal/engine"
	"sharkbite/internal/scan"
	"sharkbite/internal/sfv"
)

// Malware checks on finished torrents. The file-list check (scan.Inspect)
// runs in the engine as soon as metadata arrives; this file handles what
// happens once a torrent has finished downloading.

type scanRecord struct {
	Engine  string   `json:"engine"`
	Clean   bool     `json:"clean"`
	Threats []string `json:"threats,omitempty"`
	Error   string   `json:"error,omitempty"`
	// TooLarge: files ClamAV skipped because of their size; CanScanFully:
	// "Scan fully" would check at least one of them.
	TooLarge     []string `json:"tooLarge,omitempty"`
	CanScanFully bool     `json:"canScanFully,omitempty"`
	// Seen: the user has opened the torrent's files panel since, so its row
	// stops flagging the files that were too large.
	Seen bool  `json:"seen,omitempty"`
	At   int64 `json:"at"` // unix seconds
	// Done: the torrent's downloaded bytes when it was checked. Files ticked
	// later make it finish again with more, and it's checked again.
	Done int64 `json:"done,omitempty"`
	// SFV is the .sfv check, if the torrent has one.
	SFV *engine.SFVStatus `json:"sfv,omitempty"`
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
			if s == nil || busy || s.Scan != nil && t.Done <= s.Scan.Done {
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
	rec := &scanRecord{Engine: name, At: time.Now().Unix(), Done: t.Done}
	switch {
	case !enabled:
		rec.Error = "scanning is turned off in Settings"
	case !available:
		rec.Error = name + " isn't installed"
	default:
		if !a.runScan(ctx, t.Hash, p, false, rec) {
			return // app quitting or VPN reconnecting: try again next time
		}
	}
	// After the antivirus scan, so a torrent's .sfv can't hold it up.
	sfvRes := a.checkSFV(ctx, eng, t.Hash)
	if ctx.Err() != nil {
		return
	}
	rec.SFV = sfvRes

	a.mu.Lock()
	if s := a.savedFor(t.Hash); s != nil {
		s.Scan = rec
		a.writeSaved()
	}
	a.mu.Unlock()

	if sfvRes != nil && len(sfvRes.Bad) > 0 {
		runtime.EventsEmit(a.ctx, "error", fmt.Sprintf(
			"⚠ “%s”: %d file(s) don't match the torrent's .sfv checksums: %v", t.Name, len(sfvRes.Bad), sfvRes.Bad))
	}
	a.warnThreats(t.Name, rec)
}

// runScan scans p with the antivirus into rec, showing the torrent as
// scanning meanwhile. It returns false if ctx ended first.
func (a *App) runScan(ctx context.Context, hash, p string, full bool, rec *scanRecord) bool {
	a.mu.Lock()
	if a.scanning == nil {
		a.scanning = map[string]bool{}
	}
	a.scanning[hash] = true
	a.mu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	res, err := scan.ScanPath(sctx, p, full)
	cancel()

	a.mu.Lock()
	delete(a.scanning, hash)
	a.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		rec.Clean, rec.Threats = res.Clean, res.Threats
		rec.TooLarge, rec.CanScanFully = res.TooLarge, res.CanScanFully
	}
	return true
}

func (a *App) warnThreats(name string, rec *scanRecord) {
	if len(rec.Threats) > 0 {
		runtime.EventsEmit(a.ctx, "error", fmt.Sprintf(
			"⚠ %s found a threat in “%s”: %v. Don't open its files; consider removing it with its files.",
			rec.Engine, name, rec.Threats))
	}
}

// MarkScanSeen records that the user has seen a torrent's scan result in
// its files panel.
func (a *App) MarkScanSeen(hash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.savedFor(hash); s != nil && s.Scan != nil && !s.Scan.Seen {
		s.Scan.Seen = true
		a.writeSaved()
	}
}

// ScanFully scans a finished torrent again with the antivirus's size limits
// raised, for files the normal scan skipped. It returns at once; progress
// and the result show in the torrent list.
func (a *App) ScanFully(hash string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	p, _, ok := eng.ContentPath(hash)
	if !ok {
		return errors.New("torrent not found")
	}
	a.mu.Lock()
	ctx := a.verifyCtx
	s := a.savedFor(hash)
	if s == nil || s.Scan == nil || !s.Scan.CanScanFully || a.scanning[hash] {
		a.mu.Unlock()
		return errors.New("nothing to scan")
	}
	rec := &scanRecord{Engine: s.Scan.Engine, At: time.Now().Unix(), SFV: s.Scan.SFV, Seen: true, Done: s.Scan.Done}
	if a.scanning == nil {
		a.scanning = map[string]bool{}
	}
	a.scanning[hash] = true // now, so a second click is refused
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		if !a.runScan(ctx, hash, p, true, rec) {
			return
		}
		a.mu.Lock()
		if s := a.savedFor(hash); s != nil {
			s.Scan = rec
			a.writeSaved()
		}
		a.mu.Unlock()
		name := hash
		for _, t := range eng.List() {
			if t.Hash == hash {
				name = t.Name
			}
		}
		a.warnThreats(name, rec)
	}()
	return nil
}

// checkSFV checks the torrent's .sfv file(s), if it has any; nil if not.
// While it runs, the torrent shows as checking.
func (a *App) checkSFV(ctx context.Context, eng *engine.Engine, hash string) *engine.SFVStatus {
	files, err := eng.FilePaths(hash)
	if err != nil || len(sfv.Find(files)) == 0 {
		return nil
	}
	a.mu.Lock()
	if a.sfvChecking == nil {
		a.sfvChecking = map[string]bool{}
	}
	a.sfvChecking[hash] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.sfvChecking, hash)
		a.mu.Unlock()
	}()

	res, err := sfv.Check(ctx, files)
	st := &engine.SFVStatus{Checked: res.Checked, Bad: res.Bad, Missing: res.Missing}
	if err != nil {
		st.Error = err.Error()
	}
	return st
}

// VerifyTorrent re-checks a torrent's files on disk against its piece
// hashes and, if it has one, its .sfv. It returns at once; progress and the
// result show in the torrent list.
func (a *App) VerifyTorrent(hash string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	a.mu.Lock()
	ctx := a.verifyCtx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		res, err := eng.Verify(ctx, hash)
		if err != nil {
			if ctx.Err() == nil {
				runtime.EventsEmit(a.ctx, "error", "Verify: "+err.Error())
			}
			return
		}
		name := hash
		for _, t := range eng.List() {
			if t.Hash == hash {
				name = t.Name
			}
		}
		sfvRes := a.checkSFV(ctx, eng, hash)
		if ctx.Err() != nil {
			return
		}
		if sfvRes != nil {
			a.mu.Lock()
			if s := a.savedFor(hash); s != nil && s.Scan != nil {
				s.Scan.SFV = sfvRes
				a.writeSaved()
			} else {
				// Not finished/scanned yet: keep it out of the saved scan
				// record, or the malware scan would think it already ran.
				if a.sfvResults == nil {
					a.sfvResults = map[string]*engine.SFVStatus{}
				}
				a.sfvResults[hash] = sfvRes
			}
			a.mu.Unlock()
		}
		if res.Bad > 0 {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf(
				"⚠ “%s”: %d of %d pieces didn't match and are being downloaded again (in %v).", name, res.Bad, res.Pieces, res.BadFiles))
		}
	}()
	return nil
}

// withScans adds scan results to the torrent list for the UI.
func (a *App) withScans(list []engine.Status) []engine.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range list {
		t := &list[i]
		if s := a.savedFor(t.Hash); s != nil {
			t.Confirm = s.Confirm
		}
		if r, ok := a.sfvResults[t.Hash]; ok {
			t.SFV = r
		}
		if s := a.savedFor(t.Hash); s != nil && s.Scan != nil && s.Scan.SFV != nil {
			t.SFV = s.Scan.SFV
		}
		if a.sfvChecking[t.Hash] {
			t.SFV = &engine.SFVStatus{Checking: true}
		}
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
		case len(s.Scan.TooLarge) > 0:
			t.ScanState, t.TooLarge, t.CanScanFully = "toolarge", s.Scan.TooLarge, s.Scan.CanScanFully
			t.ScanSeen = s.Scan.Seen
		case s.Scan.Clean:
			t.ScanState = "clean"
		}
	}
	return list
}
