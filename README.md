# Torrent Client

A lightweight Windows BitTorrent client with a built-in Cloudflare WARP VPN.
Only this app goes through the VPN; the rest of the PC is unaffected.

## How it works

- **WARP registration** (`internal/warp`): registers a free WARP device on first
  run using [wgcf](https://github.com/ViRb3/wgcf)'s API client, and saves it to
  `%APPDATA%\torrent-client\warp.json`.
- **In-process tunnel** (`internal/tunnel`): runs `wireguard-go` with a userspace
  TCP/IP stack (gVisor netstack). There's no network adapter, driver or admin rights,
  and no system-wide routing.
- **Engine** (`internal/engine`): `anacrolix/torrent` with all of its own OS
  sockets disabled. Peers, HTTP/UDP trackers, DHT and webseeds are dialed through
  the tunnel, and all DNS lookups in the process are forced through it to 1.1.1.1.
  UPnP, WebRTC and inbound listening are disabled.
- **Kill switch by design**: the engine only starts after the tunnel is verified
  (`warp=on` from Cloudflare's trace), and there's no fallback network path.

The process's only real OS socket is the WireGuard UDP socket to Cloudflare.

## Limitations

- Free WARP has no port forwarding, so other peers can't connect to you.
  Downloading is fine, but seeding is limited.
- WARP hides your IP from peers but is not a privacy VPN, because Cloudflare
  knows who you are. The tunnel takes any WireGuard config, so a provider like
  Mullvad, Proton or AirVPN could be added later.
- TCP peers only (no uTP) for now.

## Development

Requires Go, Node and the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).

```
wails dev                          # live-reload dev mode
wails build                        # -> build/bin/torrent-client.exe
go run ./cmd/tunnelcheck           # verify WARP tunnel + exit IP
go run ./cmd/enginecheck <magnet|file.torrent> [seconds]   # headless download test
```

App data lives in `%APPDATA%\torrent-client`: `warp.json`, `settings.json`,
`torrents.json`, and copies of added `.torrent` files.
