# Shark BiteTorrent

A lightweight Windows BitTorrent client with a built-in VPN. Only this app goes
through the VPN; the rest of the PC is unaffected.

## Features

- **Built-in VPN, per app**: free Cloudflare WARP out of the box, or import a
  WireGuard `.conf` from any provider (Mullvad, Proton, AirVPN, ...). A forwarded
  port can be entered for providers that offer one, so peers can connect to you.
- **Kill switch by design**: torrents only start after the tunnel is verified, and
  the torrent engine has no network path except the tunnel.
- Magnet links and `.torrent` files (file picker, drag and drop, or opened from the
  browser/Explorer), choosing which files to download, pause/resume, speed limits,
  removing a torrent with or without its files, a right-click menu, and a tray icon.
- An installer that registers magnet links and `.torrent` files for the current
  user (no admin) and removes the registration on uninstall.

## How it works

- **WARP registration** (`internal/warp`): registers a free WARP device on first
  run using [wgcf](https://github.com/ViRb3/wgcf)'s API client.
- **In-process tunnel** (`internal/tunnel`): runs `wireguard-go` with a userspace
  TCP/IP stack (gVisor netstack). There's no network adapter, driver or admin rights,
  and no system-wide routing.
- **Engine** (`internal/engine`): `anacrolix/torrent` with all of its own OS
  sockets disabled. Peers, HTTP/UDP trackers, DHT and webseeds are dialed through
  the tunnel, and all DNS lookups in the process are forced through the tunnel's
  DNS server. UPnP and WebRTC are disabled.
- **Windows integration** (`internal/winassoc`, `tray.go`): HKCU file/protocol
  registration and Default Apps capabilities, plus the notification-area icon.
- **`earlyinit`**: a tiny module that switches anacrolix's file storage from mmap
  to plain I/O before the library initializes. With mmap, Windows keeps removed
  torrents' files locked until the app exits.

The process's only real OS socket is the WireGuard UDP socket to the VPN server.

## Limitations

- Free WARP has no port forwarding, so other peers can't connect to you.
  Downloading is fine, but seeding is limited.
- WARP hides your IP from peers but is not a privacy VPN, because Cloudflare
  knows who you are.
- TCP peers only (no uTP) for now.

## Development

Requires Go, Node, the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
and, for the installer, NSIS (`winget install NSIS.NSIS`).

```
wails dev                                    # live-reload dev mode
wails build -trimpath -ldflags "-s -w" -nsis -installscope user
                                             # -> build/bin/SharkBiteTorrent.exe + installer
go test ./internal/...
go run ./cmd/tunnelcheck                     # verify WARP tunnel + exit IP
go run ./cmd/enginecheck [-seconds N] [-down KB/s] [-delete] <magnet|file.torrent>
                                             # headless download test (ENGINE_STATUS=1 dumps client state)
```

The exe also takes `--register-associations` / `--unregister-associations`
(used by the installer). App data lives in `%APPDATA%\SharkBiteTorrent`:
`warp.json`, `settings.json`, `torrents.json`, `wireguard.conf` (if imported) and
copies of added `.torrent` files.
