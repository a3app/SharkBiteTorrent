# Patched copy of github.com/wailsapp/go-webview2 v1.0.22

Shark BiteTorrent uses this copy (via a `replace` in the root `go.mod`) instead of the published module, for one reason: to pass its own arguments to WebView2, the Edge component that draws the app's window.

Unpatched, WebView2 makes background requests over the normal connection, outside the app's VPN: proxy auto-discovery (a `wpad` DNS lookup) and calls to `edge.microsoft.com`. The release leak test (`cmd/leakcheck -app`) caught them. The app turns them off with browser arguments (see `webview_windows.go` in the repository root), but Wails has no option for that, and this package's loader ignores the `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` environment variable.

## The patch

- `pkg/edge/extra_args.go` (new): `var ExtraBrowserArgs []string`.
- `pkg/edge/chromium.go`: one line in `Embed`, appending `ExtraBrowserArgs` to the arguments passed to WebView2.

Everything else is the published v1.0.22, unchanged (MIT license, see LICENSE).

## When updating Wails

Wails pins a go-webview2 version. If it moves, copy the new version here, re-apply the two changes above, and run `go test ./...` (TestWebViewArgsPatched checks the patch is present) and the `cmd/leakcheck -app` release check.
