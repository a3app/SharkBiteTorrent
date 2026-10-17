//go:build !windows

package scan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"sharkbite/internal/systool"
)

// ClamAV is used on Linux and macOS when installed (clamdscan, through the
// running daemon, is much faster than clamscan, which loads its signature
// database for every run).
func clamCommand() (string, []string, bool) {
	if p, ok := systool.Find("clamdscan"); ok {
		return p, []string{"--fdpass", "--no-summary", "--infected"}, true
	}
	if p, ok := systool.Find("clamscan"); ok {
		return p, []string{"--no-summary", "--infected", "--recursive"}, true
	}
	return "", nil, false
}

// Scanner reports which antivirus ScanPath will use, if any.
func Scanner() (string, bool) {
	_, _, ok := clamCommand()
	return "ClamAV", ok
}

// ScanPath scans a file or folder with ClamAV. Exit codes: 0 clean,
// 1 something found, 2 error.
func ScanPath(ctx context.Context, p string) (Result, error) {
	res := Result{Engine: "ClamAV"}
	bin, args, ok := clamCommand()
	if !ok {
		return res, errors.New("ClamAV isn't installed")
	}
	out, err := exec.CommandContext(ctx, bin, append(args, p)...).Output()
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
	default:
		return res, fmt.Errorf("ClamAV scan failed (exit code %d)", code)
	}
	return res, nil
}
