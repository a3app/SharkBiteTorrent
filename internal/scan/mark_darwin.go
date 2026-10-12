package scan

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// MarkDownloaded sets the quarantine flag browsers set on downloads, so
// macOS Gatekeeper and XProtect check the files when they're opened.
func MarkDownloaded(p string) {
	value := []byte(fmt.Sprintf("0081;%x;Shark BiteTorrent;", time.Now().Unix()))
	eachFile(p, func(f string) {
		unix.Setxattr(f, "com.apple.quarantine", value, 0)
	})
}
