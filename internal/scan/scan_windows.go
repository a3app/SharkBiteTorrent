package scan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func mpCmdRun() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "Windows Defender", "MpCmdRun.exe")
}

// Scanner reports which antivirus ScanPath will use, if any.
func Scanner() (string, bool) {
	if _, err := os.Stat(mpCmdRun()); err != nil {
		return "Microsoft Defender", false
	}
	return "Microsoft Defender", true
}

// ScanPath scans a file or folder with Microsoft Defender. Remediation is
// disabled: Defender reports but doesn't quarantine, and the app tells the
// user (real-time protection still acts on its own as usual).
func ScanPath(ctx context.Context, p string) (Result, error) {
	res := Result{Engine: "Microsoft Defender"}
	cmd := exec.CommandContext(ctx, mpCmdRun(), "-Scan", "-ScanType", "3", "-File", p, "-DisableRemediation")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
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
	case 2:
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if name, ok := strings.CutPrefix(line, "Threat"); ok {
				if _, v, ok := strings.Cut(name, ":"); ok {
					res.Threats = append(res.Threats, strings.TrimSpace(v))
				}
			}
		}
		if len(res.Threats) == 0 {
			res.Threats = []string{"unknown threat"}
		}
	default:
		return res, fmt.Errorf("Defender scan failed (exit code %d)", code)
	}
	return res, nil
}

// MarkDownloaded tags files as downloaded from the internet (the "Mark of
// the Web" Zone.Identifier stream), so Windows SmartScreen and Defender
// check them when they're opened, as they do for browser downloads.
// Best effort: some network drives don't support the extra stream.
func MarkDownloaded(p string) {
	eachFile(p, func(f string) {
		os.WriteFile(f+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\nHostUrl=about:internet\r\n"), 0o644)
	})
}
