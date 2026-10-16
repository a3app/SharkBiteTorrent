//go:build windows

package main

import "github.com/wailsapp/go-webview2/pkg/edge"

// The app's window is drawn by Microsoft's WebView2 (Edge), which runs in its
// own processes and, left alone, makes its own background requests over the
// normal connection, outside the VPN: proxy auto-discovery (a "wpad" DNS
// lookup) and calls to edge.microsoft.com (configuration, component updates).
// The UI only loads the app's built-in pages, so none of that is needed. The
// release leak test (cmd/leakcheck -app) caught it.
//
// Wails can't pass WebView2 arguments, so this uses a small patch to
// go-webview2 (third_party/go-webview2/PATCHED.md). SmartScreen is turned off
// here too: main.go sets EnableFraudulentWebsiteDetection so Wails doesn't
// add its own --disable-features, which would replace ours (Chromium only
// uses the last one).
var webviewArgs = []string{
	"--no-proxy-server",               // no WPAD / proxy auto-detection
	"--disable-background-networking", // component updates, field trials, etc.
	"--disable-component-update",
	"--disable-domain-reliability",
	"--disable-client-side-phishing-detection",
	"--disable-sync",
	"--no-pings",
	"--metrics-recording-only", // never upload metrics
	"--disable-breakpad",
	// Text prediction / Microsoft Editor send typed text to Microsoft
	// (substrate.office.com); index.html also turns them off per page.
	"--disable-features=msSmartScreenProtection,AutofillServerCommunication,OptimizationHints,MediaRouter,Translate,msEdgeTranslate,msEdgeTextPrediction,msEnableTextPrediction,msMicrosoftEditor,msEditorProofing,msSpellCheckService",
}

func init() { edge.ExtraBrowserArgs = webviewArgs }
