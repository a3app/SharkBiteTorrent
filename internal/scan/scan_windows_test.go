package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Runs the real Microsoft Defender on a clean file and folder.
func TestDefenderClean(t *testing.T) {
	if testing.Short() {
		t.Skip("runs Defender")
	}
	if _, ok := Scanner(); !ok {
		t.Skip("Defender not installed")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "a clean file.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, p := range []string{f, dir} {
		res, err := ScanPath(ctx, p, false)
		if err != nil || !res.Clean || len(res.Threats) != 0 {
			t.Fatalf("%s: %+v, %v", p, res, err)
		}
	}
	MarkDownloaded(dir)
	if b, err := os.ReadFile(f + ":Zone.Identifier"); err != nil || len(b) == 0 {
		t.Fatalf("Mark of the Web not written: %v", err)
	}
}
