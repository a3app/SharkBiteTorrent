package scan

import (
	"io/fs"
	"path/filepath"
)

// Result of scanning finished files with the system's antivirus.
type Result struct {
	Engine  string   `json:"engine"` // e.g. "Microsoft Defender", "ClamAV"
	Clean   bool     `json:"clean"`
	Threats []string `json:"threats,omitempty"`
	// TooLarge lists files the antivirus skipped because of its size limits
	// (ClamAV). Clean then only means nothing was found in the rest.
	TooLarge []string `json:"tooLarge,omitempty"`
	// CanScanFully: a full scan (ScanPath with full set) would check at
	// least one of the TooLarge files.
	CanScanFully bool `json:"canScanFully,omitempty"`
}

// eachFile calls fn for p, or for every regular file under p if it's a folder.
func eachFile(p string, fn func(string)) {
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			fn(path)
		}
		return nil
	})
}
