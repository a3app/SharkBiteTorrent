//go:build windows

package edge

// ExtraBrowserArgs is a Shark BiteTorrent patch (see ../../PATCHED.md):
// browser arguments appended to every WebView2 environment this package
// creates. Wails doesn't expose WebView2's AdditionalBrowserArguments, and
// this package's loader deliberately ignores the
// WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS environment variable, so this is the
// only way for the app to pass its own.
var ExtraBrowserArgs []string
