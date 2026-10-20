//go:build !windows

package scan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sharkbite/internal/systool"
)

// ClamAV is used on Linux and macOS when installed. The app runs clamscan,
// not clamdscan: the daemon is off by default on many distros (Arch and
// CachyOS), its size limits are set in clamd.conf where the app can't
// raise them, and it can't report the files those limits made it skip.
//
// By default clamscan skips files over 100 MB. --alert-exceeds-max reports
// each skipped file as "Heuristics.Limits.Exceeded.…", so the app can say so
// instead of calling it clean. A full scan raises the limits to ClamAV's
// maximum; files over 2 GiB can't be scanned at all.
const fullScanLimit = 2047 << 20 // bytes; ClamAV refuses anything over 2 GiB - 1

func clamscan() (string, bool) { return systool.Find("clamscan") }

// Scanner reports which antivirus ScanPath will use, if any.
func Scanner() (string, bool) {
	_, ok := clamscan()
	return "ClamAV", ok
}

// ScanPath scans a file or folder with ClamAV. full raises its size limits
// (slow: minutes for a big ISO). Exit codes: 0 clean, 1 something found,
// 2 error, though a run that only skipped files also exits 2.
func ScanPath(ctx context.Context, p string, full bool) (Result, error) {
	res := Result{Engine: "ClamAV"}
	bin, ok := clamscan()
	if !ok {
		return res, errors.New("ClamAV isn't installed")
	}
	args := []string{"--no-summary", "--infected", "--recursive", "--alert-exceeds-max=yes"}
	if full {
		limit := fmt.Sprintf("%dM", fullScanLimit>>20)
		args = append(args, "--max-filesize="+limit, "--max-scansize="+limit)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, append(args, p)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		return res, err
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// "/path/to/file: Win.Test.EICAR_HDB-1 FOUND"
		rest, ok := strings.CutSuffix(sc.Text(), " FOUND")
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, ": ")
		if i < 0 {
			continue
		}
		file, sig := rest[:i], rest[i+2:]
		if strings.HasPrefix(sig, "Heuristics.Limits.Exceeded") {
			name := filepath.Base(file)
			if r, err := filepath.Rel(p, file); err == nil && r != "." {
				name = r
			}
			res.TooLarge = append(res.TooLarge, name)
			if fi, err := os.Stat(file); err == nil && !full && fi.Size() <= fullScanLimit {
				res.CanScanFully = true
			}
			continue
		}
		res.Threats = append(res.Threats, sig)
	}

	switch {
	case code == 1 && len(res.Threats) == 0:
		res.Threats = []string{"unknown threat"}
	case code == 2 && len(res.Threats) == 0 && len(res.TooLarge) == 0:
		if msg := firstLine(stderr.String()); msg != "" {
			return res, fmt.Errorf("ClamAV scan failed: %s", msg)
		}
		return res, fmt.Errorf("ClamAV scan failed (exit code %d)", code)
	}
	res.Clean = len(res.Threats) == 0
	return res, nil
}

// firstLine returns ClamAV's first error line, e.g. "LibClamAV Error:
// cli_loaddbdir: No supported database files found in /var/lib/clamav".
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
