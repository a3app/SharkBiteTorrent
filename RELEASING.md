# Releasing

Changes go to `main` as they're made (each user-visible one noted under **Unreleased** in [`CHANGELOG.md`](CHANGELOG.md)). A release is cut only when the maintainer decides to ship. Every push already runs [Checks](.github/workflows/checks.yml) (tests, type checks, govulncheck, npm audit) and [CodeQL](.github/workflows/codeql.yml).

## Before tagging

1. **Checks and CodeQL are green** on the commit being released, and **Security → Code scanning** has no open alerts (fix them, or dismiss them with a written reason).
2. **Leak test (engine)**, on Windows, from an **administrator** terminal (it turns on Windows' connection and DNS auditing for the run and restores the previous settings afterwards):
   ```sh
   go run ./cmd/leakcheck -selftest path/to/legal.torrent    # must print "selftest OK" (it caught both planted leaks)
   go run ./cmd/leakcheck -seconds 60 path/to/legal.torrent  # must PASS
   ```
   Every connection the OS sees from the process and everything it starts is checked (Windows Filtering Platform events 5156/5157, process creation 4688), however short, plus every lookup through the system DNS resolver (DNS Client event 3006). UDP is only allowed from the main process to the VPN server. A run whose own test connection or tunnel traffic isn't recorded fails instead of passing.
3. **Leak test (real app)**: install the build, set it to WARP, **quit it**, then from an administrator terminal run `go run ./cmd/leakcheck -app`. It launches the app itself (so startup is covered) and watches it for 150 s. The person running it: leave the window alone for the first minute, then type in the search box and search, type in the magnet box, open every Settings section (type in a text field, click through providers without saving) and check for updates. If the remote control is on, also use it from a phone on the home Wi-Fi during the run (incoming home-network connections to its port are allowed; nothing else is). Must PASS.
4. **Fresh-eyes audit of the changes**: a reviewer that hasn't seen the work (a new Claude session or agent, or a person) reviews `git diff <last-tag>..HEAD` with the brief below. Its report goes to the maintainer unedited. Required when the release touches networking, the VPN/tunnel, the updater, signing, the installer, secrets or file handling; otherwise every few releases. Every few releases, audit the whole repository instead of the diff.
5. **CHANGELOG**: rename **Unreleased** to `## [x.y.z] - YYYY-MM-DD` and make sure it says what users will notice.

## Shipping

```sh
# bump wails.json productVersion and packaging/arch/PKGBUILD pkgver, commit
git tag -a vX.Y.Z -m "Shark BiteTorrent X.Y.Z" && git push origin vX.Y.Z
# wait for the Release workflow (creates a draft), then on the maintainer's computer:
go run ./cmd/releasekey publish vX.Y.Z   # checks build provenance + hashes, signs, publishes
```

Check the draft's release notes before publishing.

`publish` also downloads the Microsoft Store package (`.msix`) from that release run, checks its build provenance the same way (nothing is published if it fails), and puts it in your Downloads folder. Upload that file in Partner Center: the app → **Packages** → drag it in → **Submit**. (Store API automation isn't available on individual developer accounts; and keeping the upload on the maintainer's computer means a hijacked GitHub account can't push a Store update either.)

## Audit brief

> You are auditing Shark BiteTorrent, a BitTorrent client whose core promise is that only this app's traffic uses a VPN, and that nothing (torrents, DNS, trackers, search, updates, provider APIs) leaks outside it unless the user explicitly chose "No VPN". Review `git diff <last-tag>..HEAD` (or the whole repository if asked). Look for: traffic that can leave outside the tunnel; secrets (keys, passwords, API keys) that could be logged, shown or sent somewhere; files read, written or deleted outside where they should be; anything that runs other programs; weaknesses in the updater, signing or installer; supply-chain risks in dependencies and workflows; and claims in README.md, PRIVACY.md or the app's text that the code doesn't back up. Assume the author missed things. For each finding give the file and line, what goes wrong in a concrete scenario, and a suggested fix. Say plainly when you found nothing in an area, and what you didn't review.
