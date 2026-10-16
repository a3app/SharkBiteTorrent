# Releasing

Changes go to `main` as they're made (each user-visible one noted under **Unreleased** in [`CHANGELOG.md`](CHANGELOG.md)). A release is cut only when the maintainer decides to ship. Every push already runs [Checks](.github/workflows/checks.yml) (tests, type checks, govulncheck, npm audit) and [CodeQL](.github/workflows/codeql.yml).

## Before tagging

1. **Checks and CodeQL are green** on the commit being released, and **Security → Code scanning** has no open alerts (fix them, or dismiss them with a written reason).
2. **Leak test (engine)**, in VPN mode, on Windows:
   ```sh
   go run ./cmd/leakcheck -seconds 60 path/to/legal.torrent    # must print PASS
   go run ./cmd/leakcheck -selftest -seconds 5 path/to/legal.torrent   # must FAIL (proves the check works)
   ```
3. **Leak test (real app)**: install the build, start it on WARP, run `scripts/connlog.sh 120`, and meanwhile use the app: add a torrent, search, open every Settings section, check for updates. Must print PASS.
4. **Fresh-eyes audit of the changes**: a reviewer that hasn't seen the work (a new Claude session or agent, or a person) reviews `git diff <last-tag>..HEAD` with the brief below. Its report goes to the maintainer unedited. Required when the release touches networking, the VPN/tunnel, the updater, signing, the installer, secrets or file handling; otherwise every few releases. Every few releases, audit the whole repository instead of the diff.
5. **CHANGELOG**: rename **Unreleased** to `## [x.y.z] - YYYY-MM-DD` and make sure it says what users will notice.

## Shipping

```sh
# bump wails.json productVersion and packaging/arch/PKGBUILD pkgver, commit
git tag -a vX.Y.Z -m "Shark BiteTorrent X.Y.Z" && git push origin vX.Y.Z
# wait for the Release workflow (creates a draft), then on the maintainer's computer:
go run ./cmd/releasekey publish vX.Y.Z   # checks build provenance + hashes, signs, publishes
```

Check the draft's release notes before publishing. For the Microsoft Store, upload that run's **msix** artifact in Partner Center.

## Audit brief

> You are auditing Shark BiteTorrent, a BitTorrent client whose core promise is that only this app's traffic uses a VPN, and that nothing (torrents, DNS, trackers, search, updates, provider APIs) leaks outside it unless the user explicitly chose "No VPN". Review `git diff <last-tag>..HEAD` (or the whole repository if asked). Look for: traffic that can leave outside the tunnel; secrets (keys, passwords, API keys) that could be logged, shown or sent somewhere; files read, written or deleted outside where they should be; anything that runs other programs; weaknesses in the updater, signing or installer; supply-chain risks in dependencies and workflows; and claims in README.md, PRIVACY.md or the app's text that the code doesn't back up. Assume the author missed things. For each finding give the file and line, what goes wrong in a concrete scenario, and a suggested fix. Say plainly when you found nothing in an area, and what you didn't review.
