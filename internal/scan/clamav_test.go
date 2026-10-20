//go:build !windows

package scan

import (
	"reflect"
	"testing"
)

// Output from real clamscan 1.5 runs with --infected --alert-exceeds-max.
func TestParseClamscan(t *testing.T) {
	cases := []struct {
		name     string
		out      string
		code     int
		stderr   string
		clean    bool
		threats  []string
		tooLarge []string
		wantErr  bool
	}{
		{name: "clean", code: 0, clean: true},
		{name: "threat", code: 1, out: "/d/eicar.com: Eicar-Test-Signature FOUND\n", threats: []string{"Eicar-Test-Signature"}},
		// A Linux ISO whose unpacked filesystem passes the scan-size limit exits 1.
		{name: "scan size limit, exit 1", code: 1, out: "/d/arch.iso: Heuristics.Limits.Exceeded.MaxScanSize FOUND\n", clean: true, tooLarge: []string{"arch.iso"}},
		{name: "file size limit, exit 2", code: 2, out: "/d/big.iso: Heuristics.Limits.Exceeded.MaxFileSize FOUND\n", clean: true, tooLarge: []string{"big.iso"}},
		{name: "threat and limit", code: 1, out: "/d/a.exe: Win.Trojan.X FOUND\n/d/big.iso: Heuristics.Limits.Exceeded.MaxFileSize FOUND\n", threats: []string{"Win.Trojan.X"}, tooLarge: []string{"big.iso"}},
		{name: "found, nothing printed", code: 1, threats: []string{"unknown threat"}},
		{name: "error", code: 2, stderr: "LibClamAV Error: cli_loaddbdir: No supported database files found in /var/lib/clamav\n", wantErr: true},
	}
	for _, c := range cases {
		res, err := parseClamscan(Result{Engine: "ClamAV"}, []byte(c.out), c.code, c.stderr, "/d", false)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if res.Clean != c.clean || !reflect.DeepEqual(res.Threats, c.threats) || !reflect.DeepEqual(res.TooLarge, c.tooLarge) {
			t.Errorf("%s: got %+v", c.name, res)
		}
	}
}
