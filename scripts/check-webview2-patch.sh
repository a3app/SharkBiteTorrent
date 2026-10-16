#!/usr/bin/env bash
# Checks that third_party/go-webview2 is exactly the published go-webview2
# version go.mod requires, plus the documented patch (PATCHED.md) and nothing
# else. Run from the repo root; CI runs it on every push.
set -euo pipefail

ver=$(sed -n 's#^[[:space:]]*github.com/wailsapp/go-webview2 \(v[^ ]*\).*#\1#p' go.mod | head -1)
[ -n "$ver" ] || { echo "go.mod doesn't require github.com/wailsapp/go-webview2" >&2; exit 1; }
grep -q "go-webview2 $ver" third_party/go-webview2/PATCHED.md || {
  echo "go.mod requires go-webview2 $ver, but third_party/go-webview2 is a patched copy of another version (see its PATCHED.md); re-apply the patch to $ver" >&2
  exit 1
}

upstream=$(go mod download -json "github.com/wailsapp/go-webview2@$ver" | sed -n 's/.*"Dir": "\(.*\)".*/\1/p' | sed 's#\\\\#/#g')
[ -d "$upstream" ] || { echo "couldn't download go-webview2 $ver" >&2; exit 1; }

problems=0
# Every file must match upstream (ignoring line endings), except the patch.
while IFS= read -r line; do
  case "$line" in
    "Only in third_party/go-webview2: PATCHED.md") ;;
    "Only in third_party/go-webview2/pkg/edge: extra_args.go") ;;
    "Files "*"/pkg/edge/chromium.go and third_party/go-webview2/pkg/edge/chromium.go differ") ;;
    *) echo "unexpected difference from upstream: $line" >&2; problems=1 ;;
  esac
done < <(diff -rq --strip-trailing-cr "$upstream" third_party/go-webview2 || true)

# extra_args.go: exactly the reviewed content (line endings ignored).
want_extra=901c2c2139bf769ed4990d6871a8eed55d1b6ac4811c27f0a80e3d24ffd14c71
got_extra=$(tr -d '\r' < third_party/go-webview2/pkg/edge/extra_args.go | sha256sum | cut -c1-64)
if [ "$got_extra" != "$want_extra" ]; then
  echo "pkg/edge/extra_args.go isn't the reviewed version (sha256 $got_extra); if you changed it on purpose, review it and update want_extra" >&2
  problems=1
fi

# chromium.go: exactly one changed line, and it's exactly the patch line.
want_line='> 	browserArgs := strings.Join(append(append([]string{}, e.AdditionalBrowserArgs...), ExtraBrowserArgs...), " ") // SharkBite patch, see extra_args.go'
cdiff=$(diff --strip-trailing-cr "$upstream/pkg/edge/chromium.go" third_party/go-webview2/pkg/edge/chromium.go || true)
added=$(printf '%s\n' "$cdiff" | grep -c '^>' || true)
removed=$(printf '%s\n' "$cdiff" | grep -c '^<' || true)
if [ "$added" != "1" ] || [ "$removed" != "1" ] || [ "$(printf '%s\n' "$cdiff" | grep '^>')" != "$want_line" ]; then
  echo "pkg/edge/chromium.go differs from upstream by more than the documented one-line patch" >&2
  problems=1
fi

[ "$problems" = 0 ] && echo "third_party/go-webview2 = upstream $ver + the documented patch"
exit "$problems"
