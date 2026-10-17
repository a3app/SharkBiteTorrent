#!/usr/bin/env bash
# Builds the Microsoft Store package (dist/SharkBiteTorrent-<version>.msix)
# from the repo root. It's unsigned on purpose: Partner Center signs it.
#
#   packaging/msix/build-msix.sh [path-to-sdk-bin-x64]
#
# Needs wails, and makeappx.exe + makepri.exe from the Windows SDK (or the
# Microsoft.Windows.SDK.BuildTools NuGet package). Pass their folder, or it
# looks in the installed Windows SDK.
set -euo pipefail

sdk="${1:-}"
if [ -z "$sdk" ]; then
  sdk=$(ls -d "/c/Program Files (x86)/Windows Kits/10/bin/"10.*/x64 2>/dev/null | sort -V | tail -1)
fi
[ -x "$sdk/makeappx.exe" ] || { echo "makeappx.exe not found (pass the SDK bin/x64 folder)" >&2; exit 1; }

version=$(sed -n 's/.*"productVersion": "\([0-9.]*\)".*/\1/p' wails.json)
# The Store wants four parts with the last one 0.
msixver="$version.0"

# Pure Go: a Store package can't load DLLs from anywhere but itself and
# Windows, so MinGW's runtime (linked when cgo is on) would stop it starting.
CGO_ENABLED=0 wails build -clean -trimpath -ldflags "-s -w" -tags store
go run scripts/check-pe-imports.go build/bin/SharkBiteTorrent.exe

layout=build/msix
rm -rf "$layout" && mkdir -p "$layout" dist
cp build/bin/SharkBiteTorrent.exe LICENSE THIRD_PARTY_LICENSES.txt "$layout/"
cp -r packaging/msix/Assets "$layout/"
sed "s/Version=\"VERSION\"/Version=\"$msixver\"/" packaging/msix/AppxManifest.xml > "$layout/AppxManifest.xml"

# resources.pri lets Windows pick the right logo size (scale/targetsize).
(cd "$layout" &&
  "$sdk/makepri.exe" createconfig //cf priconfig.xml //dq en-US //o >/dev/null &&
  # One resources.pri with every size, not split per scale (that's for bundles).
  sed -i '/<packaging>/,/<\/packaging>/d' priconfig.xml &&
  "$sdk/makepri.exe" new //pr . //cf priconfig.xml //mn AppxManifest.xml //of resources.pri //o >/dev/null &&
  rm priconfig.xml)

out="dist/SharkBiteTorrent-$version.msix"
"$sdk/makeappx.exe" pack //d "$layout" //p "$out" //o | tr -d '\000' | grep -iE "error|warning" || true
[ -f "$out" ] || { echo "makeappx failed" >&2; exit 1; }
echo "Built $out"
