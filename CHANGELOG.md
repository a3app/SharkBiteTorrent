# Changelog

What changed in each release of Shark BiteTorrent. The release notes on GitHub are taken from this file, so every release's section should say what users will notice.

## [Unreleased]

### Added
- **Release notes in the app.** When an update is available, *What's new* in the update banner (and in Settings → About) lists every change since your version, right in the app. Settings → About also has *What's new* for the version you're running.

### Fixed
- **Privacy:** opening Settings → VPN no longer contacts a VPN provider over your normal connection. Server lists load only when *VPN account* is selected, and while any VPN is connected, provider requests (server lists, sign-in, sign-out) go through it. They go direct only when there's no tunnel yet, i.e. while connecting. Those direct connections are also closed right away instead of lingering. Found by an independent audit.

## [0.2.30] - 2026-10-16

### Security
- Releases are now checked before they're signed: every file must provably have been built by the project's own release workflow from the exact source code being released (GitHub build provenance). Files swapped into a release, or built from anything else, are refused. Thanks to the independent reviewer who spotted the gap.

### Changed
- The **No VPN** warning now mentions that, like other torrent clients, it may ask your router (UPnP / NAT-PMP) to open a port.

## [0.2.29] - 2026-10-16

### Added
- **Check it's working** (Settings → Search, under the proxy option): runs a test search and confirms from the actual traffic that Prowlarr or Jackett sends its searches through the VPN. Especially useful for Jackett, whose proxy you set up by hand.

## [0.2.28] - 2026-10-16

### Added
- **No VPN** option (Settings → VPN), for people whose router or computer already runs a VPN, or who only download legal content. It's never used automatically: if a VPN fails, downloads still stop. The header shows an amber **VPN off** while it's chosen.
- Search keeps working without the VPN. The Prowlarr/Jackett proxy refuses connections in this mode (so a proxied search server never switches to your real IP), and Settings explains how to let it search directly.

## [0.2.27] - 2026-10-16

### Added
- **Verify files** in the right-click menu: re-reads a torrent's files from disk and checks every piece against the torrent's hashes, catching damage after downloading (a bad copy, a failing drive or NAS). Bad pieces are downloaded again automatically.
- Torrents that include `.sfv` files are checked against them (CRC32 per file) when they finish, and again with Verify files.

## [0.2.26] - 2026-10-15

### Security
- **Signed releases.** Updates are signed with a key that never leaves the maintainer's computer, and the app refuses any update without a valid signature for that exact version, so even a hijacked GitHub account couldn't push an update the app would install. You can check a download yourself with `minisign` and `release.pub`.

## [0.2.25] - 2026-10-15

### Security
- Search downloads that point at an indexer site (instead of your own Prowlarr/Jackett) now go through the VPN, not your normal connection.
- The Windows uninstaller now removes only the files it installed. Before, uninstalling from a custom folder (like Downloads) could delete everything in that folder.
- Update downloads check the version format; the release workflow's actions are pinned to exact versions; library updates.

## [0.2.24] - 2026-10-15

### Added
- Microsoft Store packaging (the Store version gets its updates from the Store).
- A privacy policy (`PRIVACY.md`).

## [0.2.23] - 2026-10-15

### Added
- **Support development** button in Settings → About, now that GitHub Sponsors is live. Entirely optional; builds made with `-tags nofunding` don't have it.

## [0.2.22] - 2026-10-14

### Fixed
- Removing a selected torrent no longer shows a "torrent not found" error.

## [0.2.21] - 2026-10-14

### Fixed
- Show/Hide on the proxy password no longer collapses the proxy details.

## [0.2.20] - 2026-10-14

### Added
- Optional: send **Prowlarr's or Jackett's own traffic through the VPN** too (Settings → Search). The app sets Prowlarr up for you; Jackett shows the values to enter.

## [0.2.19] - 2026-10-14

### Fixed
- If Cloudflare drops the saved WARP device, the app registers a new one instead of failing until you delete its settings.
- PIA port forwarding renews itself before it expires, and its warning clears once it works again.
- Signing out of Mullvad removes the device from your account, so you don't run into Mullvad's 5-device limit.
- The Linux download now runs on older distributions (Debian, Ubuntu LTS) as well as Arch/CachyOS.
- macOS instructions updated for the current "Open Anyway" step.

## [0.2.18] - 2026-10-14

### Security
- Updated Go and two networking libraries to fix known vulnerabilities.

## [0.2.17] - 2026-10-14

### Fixed
- Downloading a search result from Prowlarr no longer fails with "The filename, directory name, or volume label syntax is incorrect".
- Error messages no longer show your Prowlarr/Jackett API key.

## [0.2.16] - 2026-10-14

### Changed
- Every column in the search results can be sorted (including Peers and Source).

## [0.2.15] - 2026-10-14

### Fixed
- Search results not appearing, with the button stuck on "Searching…".

## [0.2.14] - 2026-10-14

### Fixed
- Search couldn't reach Prowlarr/Jackett on this computer ("couldn't reach localhost") while the VPN was up.

## [0.2.13] - 2026-10-14

### Added
- **Search** through your own Prowlarr or Jackett, which the app can find on this computer automatically. No sites are built in.

## [0.2.12] - 2026-10-12

### Added
- **Malware checks**: torrents are checked for disguised programs as soon as their file list arrives, and finished downloads are marked as downloaded from the internet and scanned with Microsoft Defender (or ClamAV on Linux/macOS).

## [0.2.11] - 2026-10-12

### Fixed
- **Show files** on a single file whose path has spaces opened Documents instead of its folder.

## [0.2.10] - 2026-10-12

### Fixed
- Piece map flicker.

## [0.2.9] - 2026-10-12

### Fixed
- Piece map flashing once a second.

## [0.2.8] - 2026-10-12

### Fixed
- Updating could silently keep the old version installed.

## [0.2.7] - 2026-10-12

### Added
- **Piece map** in the torrent details: which pieces are done, downloading, being checked, missing or skipped.

## [0.2.6] - 2026-10-12

### Changed
- Much faster downloads to network drives (NAS).
- Download progress survives restarts.

### Fixed
- **Show files** for single-file torrents.

## [0.2.5] - 2026-10-12

### Changed
- Up to 200 peers per torrent (effectively unlimited); use the speed limits to control bandwidth.

## [0.2.4] - 2026-10-12

### Changed
- Connects to peers much faster.

### Added
- `-tags nofunding` build option that strips the sponsor and referral links.

## [0.2.3] - 2026-10-12

### Fixed
- Drag and drop of `.torrent` files; magnet links dragged from a browser are accepted.

## [0.2.2] - 2026-10-12

### Added
- Update checks and in-app updates.
- IPv6 trackers.
- A clear message when a private torrent can't start from a magnet link.

## [0.2.1] - 2026-10-12

### Fixed
- DHT and UDP trackers never working inside the VPN tunnel (much better peer discovery).

## [0.2.0] - 2026-10-12

First public release: a lightweight BitTorrent client with a built-in VPN that only the torrent client uses. Free Cloudflare WARP by default, or sign in to Mullvad, PIA (with port forwarding), NordVPN or IVPN, or import any WireGuard config. Windows, Linux and macOS.
