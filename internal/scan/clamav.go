//go:build !windows

package scan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"sharkbite/internal/systool"
)

type clamCmd struct {
	bin  string
	args []string
}

// ClamAV is used on Linux and macOS when installed. clamdscan, through the
// running daemon, is much faster than clamscan, which loads its signature
// database for every run. Distros install clamdscan with ClamAV but leave the
// daemon off (Arch/CachyOS do), so clamscan is the fallback when it fails.
func clamCommands() []clamCmd {
	var cmds []clamCmd
	if p, ok := systool.Find("clamdscan"); ok {
		cmds = append(cmds, clamCmd{p, []string{"--fdpass", "--no-summary", "--infected"}})
	}
	if p, ok := systool.Find("clamscan"); ok {
		cmds = append(cmds, clamCmd{p, []string{"--no-summary", "--infected", "--recursive"}})
	}
	return cmds
}

// Scanner reports which antivirus ScanPath will use, if any.
func Scanner() (string, bool) {
	return "ClamAV", len(clamCommands()) > 0
}

// ScanPath scans a file or folder with ClamAV. Exit codes: 0 clean,
// 1 something found, 2 error (then the next command is tried).
func ScanPath(ctx context.Context, p string) (Result, error) {
	res := Result{Engine: "ClamAV"}
	cmds := clamCommands()
	if len(cmds) == 0 {
		return res, errors.New("ClamAV isn't installed")
	}
	var lastErr error
	for _, c := range cmds {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, c.bin, append(c.args, p)...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			return res, err
		}
		switch code {
		case 0:
			res.Clean = true
			return res, nil
		case 1:
			sc := bufio.NewScanner(strings.NewReader(string(out)))
			for sc.Scan() {
				// "/path/to/file: Win.Test.EICAR_HDB-1 FOUND"
				line := sc.Text()
				if rest, ok := strings.CutSuffix(line, " FOUND"); ok {
					if i := strings.LastIndex(rest, ": "); i >= 0 {
						res.Threats = append(res.Threats, rest[i+2:])
					}
				}
			}
			if len(res.Threats) == 0 {
				res.Threats = []string{"unknown threat"}
			}
			return res, nil
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		lastErr = fmt.Errorf("ClamAV scan failed (exit code %d)", code)
		if msg := firstLine(stderr.String()); msg != "" {
			lastErr = fmt.Errorf("ClamAV scan failed: %s", msg)
		}
	}
	return res, lastErr
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
