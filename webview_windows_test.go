//go:build windows

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/wailsapp/go-webview2/pkg/edge"
)

// The go-webview2 patch must stay in place (third_party/go-webview2), or
// WebView2's background traffic leaves outside the VPN again.
func TestWebViewArgsPatched(t *testing.T) {
	if len(edge.ExtraBrowserArgs) == 0 {
		t.Fatal("edge.ExtraBrowserArgs not set")
	}
	src, err := os.ReadFile("third_party/go-webview2/pkg/edge/chromium.go")
	const patch = `browserArgs := strings.Join(append(append([]string{}, e.AdditionalBrowserArgs...), ExtraBrowserArgs...), " ")`
	if err != nil || !strings.Contains(string(src), patch) {
		t.Fatal("the go-webview2 patch is missing from chromium.go (see third_party/go-webview2/PATCHED.md)")
	}
	mod, _ := os.ReadFile("go.mod")
	if !strings.Contains(string(mod), "github.com/wailsapp/go-webview2 => ./third_party/go-webview2") {
		t.Fatal("go.mod doesn't replace go-webview2 with the patched copy")
	}
}
