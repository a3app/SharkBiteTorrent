# Privacy policy

*Shark BiteTorrent, published by SharkByte Software. Last updated October 16, 2026.*

Shark BiteTorrent doesn't collect, store or share any information about you. There is no account with us, no analytics, no crash reporting and no ads, and the developers run no server for the app to send data to.

## What stays on your computer

Everything the app keeps is stored only on your own device:

- your settings and download list;
- your VPN sign-ins or imported WireGuard configuration, encrypted with your operating system's protection (Windows DPAPI, or the macOS/Linux keychain);
- the address and API key of your own Prowlarr or Jackett, if you connect one (also encrypted).

Uninstalling the app and deleting its settings folder removes all of it.

## Who the app talks to

Normally everything below goes through the VPN. The exceptions are spelled out: what has to happen before a VPN exists, and what you choose with **No VPN**.

- **The BitTorrent network** (peers, trackers, DHT and web seeds) for the torrents you add. Always through the VPN; if the VPN fails, downloads stop. Only if you choose *No VPN* does this use your normal connection, and then, like other torrent clients, the app may ask your router (UPnP / NAT-PMP) to open a port.
- **Your VPN provider:** Cloudflare WARP by default, or the provider you sign in to (Mullvad, Private Internet Access, NordVPN, IVPN), using that provider's own API. Their privacy policy covers what they see.
  - While a VPN is connected, requests to providers (server lists, sign-in, sign-out, setting up a switch to another provider) go through it. If setting up a switch through the current VPN fails while that VPN still works, the app stays on it and tells you, rather than contacting the new provider directly. If the current VPN has stopped working, there's no VPN to go through, so the switch continues as a first connection would.
  - With no VPN connected (the first connection, after a failed connection, or with *No VPN*), the app contacts providers directly only for what you do with them: signing in to or connecting to one (which sees your IP address when you connect anyway), signing out of one, or showing the server locations of one you're signed in to. Registering a free WARP account also goes direct: once at first launch, and again whenever connecting with the saved account fails (for example after Cloudflare drops it, or when the internet was down).
  - Opening Settings or browsing the list of providers doesn't contact any of them.
- **Cloudflare's public trace page** (`cloudflare.com/cdn-cgi/trace`), through the VPN to check it works. When connecting to a provider other than WARP, it's also loaded once over your normal connection, to confirm the VPN's address differs from yours and to pick a server in your country.
- **GitHub**, to check for new versions: about once a day through the VPN, and you can turn this off in Settings. A manual *Check for updates* while the VPN is down, or any check with *No VPN* chosen, uses your normal connection. When a new version is available, its release notes are also loaded from `raw.githubusercontent.com` the same way. The Microsoft Store version gets updates from the Store instead and doesn't check GitHub.
- **Your own Prowlarr or Jackett**, only if you set up search. Downloading a search result goes through your Prowlarr/Jackett, or through the VPN for links to other sites (directly with *No VPN*). If you turn on the search proxy, the app changes Prowlarr's proxy setting through Prowlarr's API, and changes it back when you turn the proxy off.

On Windows, the app's window is drawn by Microsoft WebView2 (the engine of Microsoft Edge), which on its own would contact Microsoft: looking up your Windows account and signing in to it, text prediction and Microsoft Editor (which send what you type), proxy auto-discovery, Edge configuration and component updates. The app turns those features off and blocks the window's own network layer (it never needs the network). The release leak test checks the app and every process it starts; Windows' own sign-in broker runs separately, so the test reports its activity rather than counting it. Windows may also update the WebView2 runtime through its own update service, outside the app. On macOS and Linux the window uses the system's own web view, which hasn't been checked the same way yet.

**Remote control** (off unless you turn it on in Settings): the app then also accepts connections from devices on your home network, on the port shown in Settings (8732 by default), and only from private network addresses. Paired phones get a random access key, stored on this computer only as a hash. The page uses plain HTTP, so someone else on your home Wi-Fi could in principle see that traffic. It never accepts connections through the VPN or from the internet.

None of these connections carry information about you to the developers.

## Links in the app

Settings has optional links to the project's GitHub Sponsors page and to VPN providers' sign-up pages, some of which are marked as referral links. They only open in your browser when you click them, and the app doesn't track whether you do.

## Children

The app doesn't knowingly collect information from anyone, including children.

## Changes and contact

Changes to this policy are made in this file, and its history is public on GitHub. Questions: open an issue at <https://github.com/trevercomb/SharkBiteTorrent/issues>.
