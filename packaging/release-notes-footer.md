## Downloads

| Platform | File |
|---|---|
| **Windows** (recommended) | `SharkBiteTorrent-amd64-installer.exe` (registers magnet links and .torrent files) |
| Windows (portable) | `SharkBiteTorrent.exe` |
| **Linux: Arch / CachyOS** | `sharkbitetorrent-*.pkg.tar.zst`, install with `sudo pacman -U <file>` |
| Linux (other) | `SharkBiteTorrent-*-linux-x86_64.tar.gz` (needs GTK3 and WebKitGTK 4.1) |
| **macOS** (Intel + Apple Silicon) | `SharkBiteTorrent-macos-universal.zip` |

The macOS app isn't signed with an Apple developer certificate. The first time you
open it, macOS blocks it: go to **System Settings → Privacy & Security** and click
**Open Anyway**, or run `xattr -cr "/Applications/Shark BiteTorrent.app"`.

Linux and macOS builds are new and less tested than Windows.

**Verify a download:** the app does this for you when it updates (Windows) or downloads an update (Linux, macOS).
By hand: `SHA256SUMS.txt` is signed with the project's release key, ID `F59273E544BB8BA3`
([`release.pub`](https://github.com/trevercomb/SharkBiteTorrent/blob/main/release.pub); keep the copy you first trusted,
and check that a new one has the same ID). With [minisign](https://jedisct1.github.io/minisign/), run
`minisign -Vm SHA256SUMS.txt -p release.pub`, then `sha256sum -c --ignore-missing SHA256SUMS.txt`.
