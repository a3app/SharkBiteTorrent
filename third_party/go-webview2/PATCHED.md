# Patched copy of github.com/wailsapp/go-webview2 v1.0.22

Shark BiteTorrent uses this copy (via a `replace` in the root `go.mod`) instead of the published module, for one reason: to pass its own arguments to WebView2, the Edge component that draws the app's window.

Unpatched, WebView2 contacts Microsoft over the normal connection, outside the app's VPN: at startup it looks up the Windows account at `substrate.office.com` (implicit sign-in) and calls `edge.microsoft.com`, it does proxy auto-discovery (a `wpad` DNS lookup), and typing triggers Microsoft's text prediction service. The release leak test (`cmd/leakcheck -app`) caught all of these. The app turns them off with browser arguments (see `webview_windows.go` in the repository root): the real Edge feature switches, plus host-resolver rules and a dead proxy that block WebView2's network layer. Wails has no option for passing arguments, and this package's loader ignores the `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` environment variable.

## The patch

- `pkg/edge/extra_args.go` (new): `var ExtraBrowserArgs []string`.
- `pkg/edge/chromium.go`: one line in `Embed`, appending `ExtraBrowserArgs` to the arguments passed to WebView2.

Everything else is the published v1.0.22, unchanged (MIT license, see LICENSE).

## When updating Wails

Wails pins a go-webview2 version. If it moves, copy the new version here, re-apply the two changes above, and update the expected line and `extra_args.go` hash in `scripts/check-webview2-patch.sh` (CI runs it), run `go test ./...` (TestWebViewArgsPatched), and the `cmd/leakcheck -app` release check.
