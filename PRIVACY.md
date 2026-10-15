# Privacy policy

*Shark BiteTorrent, published by SharkByte Software. Last updated October 15, 2026.*

Shark BiteTorrent doesn't collect, store or share any information about you. There is no account with us, no analytics, no crash reporting and no ads, and the developers run no server for the app to send data to.

## What stays on your computer

Everything the app keeps is stored only on your own device:

- your settings and download list;
- your VPN sign-ins or imported WireGuard configuration, encrypted with your operating system's protection (Windows DPAPI, or the macOS/Linux keychain);
- the address and API key of your own Prowlarr or Jackett, if you connect one (also encrypted).

Uninstalling the app and deleting its settings folder removes all of it.

## Who the app talks to

The app only connects to:

- **The BitTorrent network** (peers, trackers, DHT and web seeds) for the torrents you add. This traffic always goes through the VPN.
- **Your VPN provider:** Cloudflare WARP by default, or the provider you sign in to (Mullvad, Private Internet Access, NordVPN, IVPN), using that provider's own API to connect. Their privacy policy covers what they see.
- **Cloudflare's public trace page** (`cloudflare.com/cdn-cgi/trace`), to check that traffic leaves through the VPN.
- **GitHub**, about once a day and through the VPN, to check for new versions. You can turn this off in Settings. The Microsoft Store version gets updates from the Store instead and doesn't check GitHub.
- **Your own Prowlarr or Jackett**, only if you set up search. If you turn on the search proxy, the app changes Prowlarr's proxy setting through Prowlarr's API, and changes it back when you turn the proxy off.

None of these connections carry information about you to the developers.

## Links in the app

Settings has optional links to the project's GitHub Sponsors page and to VPN providers' sign-up pages, some of which are marked as referral links. They only open in your browser when you click them, and the app doesn't track whether you do.

## Children

The app doesn't knowingly collect information from anyone, including children.

## Changes and contact

Changes to this policy are made in this file, and its history is public on GitHub. Questions: open an issue at <https://github.com/trevercomb/SharkBiteTorrent/issues>.
