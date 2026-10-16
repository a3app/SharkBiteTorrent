package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aead.dev/minisign"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Update checks use GitHub's public "latest release" redirect instead of the
// REST API: the API allows 60 requests/hour per IP, and WARP users share IPs.
// Checks and downloads go through the VPN when it's up.

const (
	releasesURL     = "https://github.com/trevercomb/SharkBiteTorrent/releases"
	installerAsset  = "SharkBiteTorrent-amd64-installer.exe"
	checksumsAsset  = "SHA256SUMS.txt"
	signatureAsset  = "SHA256SUMS.txt.minisig"
	autoCheckPeriod = 20 * time.Hour
)

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	NotesURL  string `json:"notesURL"`
	// CanInstall is true where the app can download and run the installer
	// itself (the installed Windows version). Elsewhere the UI links to the
	// release page.
	CanInstall bool `json:"canInstall"`
	// Direct is true if the check went over the normal connection because
	// the VPN wasn't up.
	Direct bool `json:"direct"`
}

func (a *App) currentVersion() string { return a.GetAbout().Version }

// updateClient returns an HTTP client through the VPN, or the direct
// connection if allowDirect and the VPN is down.
func (a *App) updateClient(allowDirect bool) (*http.Client, bool, error) {
	a.mu.Lock()
	tun := a.tun
	a.mu.Unlock()
	if tun != nil {
		c := tun.HTTPClient()
		c.Timeout = 10 * time.Minute // installer downloads
		return c, false, nil
	}
	if !allowDirect {
		return nil, false, errors.New("VPN not connected")
	}
	return &http.Client{Timeout: 10 * time.Minute}, true, nil
}

func (a *App) checkUpdate(ctx context.Context, allowDirect bool) (UpdateInfo, error) {
	info := UpdateInfo{Current: a.currentVersion(), CanInstall: a.canInstallUpdates()}
	hc, direct, err := a.updateClient(allowDirect)
	if err != nil {
		return info, err
	}
	info.Direct = direct

	// /releases/latest redirects to /releases/tag/vX.Y.Z; read the tag from
	// the redirect without following it.
	noFollow := *hc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesURL+"/latest", nil)
	if err != nil {
		return info, err
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		return info, fmt.Errorf("couldn't reach GitHub: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return info, fmt.Errorf("no releases found (HTTP %d)", resp.StatusCode)
	}
	tag := loc[i+len("/tag/"):]
	info.Latest = strings.TrimPrefix(tag, "v")
	info.NotesURL = releasesURL + "/tag/" + tag
	info.Available = newerVersion(info.Latest, info.Current)
	return info, nil
}

// newerVersion reports whether a > b for dotted numeric versions ("0.2.10" > "0.2.9").
// A pre-release suffix ("0.3.0-rc1") sorts before the release it leads up to.
func newerVersion(a, b string) bool {
	a, preA, _ := strings.Cut(a, "-")
	b, preB, _ := strings.Cut(b, "-")
	if a == b || !newerCore(a, b) && !newerCore(b, a) {
		switch {
		case preA == preB:
			return false
		case preA == "":
			return true // release beats its own pre-release
		case preB == "":
			return false
		}
		return preA > preB
	}
	return newerCore(a, b)
}

func newerCore(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// autoCheckUpdate runs after the VPN connects: at most once per period,
// through the tunnel only, and only if the user hasn't turned it off.
func (a *App) autoCheckUpdate() {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if storeBuild || !s.AutoUpdateCheck || time.Since(time.Unix(s.LastUpdateCheck, 0)) < autoCheckPeriod {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := a.checkUpdate(ctx, false)
	if err != nil {
		return // quiet; the user can check manually
	}
	a.mu.Lock()
	a.settings.LastUpdateCheck = time.Now().Unix()
	skipped := a.settings.SkippedVersion
	a.mu.Unlock()
	a.writeSettings()
	if info.Available && info.Latest != skipped {
		runtime.EventsEmit(a.ctx, "update-available", info)
	}
}

// --- Bound to the frontend ---

// CheckForUpdates is the manual "Check for updates" button. It uses the VPN
// if it's up, and otherwise the normal connection (reported via Direct).
func (a *App) CheckForUpdates() (UpdateInfo, error) {
	if storeBuild {
		return UpdateInfo{}, errors.New("the Microsoft Store installs updates for this version")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := a.checkUpdate(ctx, true)
	if err == nil {
		a.mu.Lock()
		a.settings.LastUpdateCheck = time.Now().Unix()
		a.mu.Unlock()
		a.writeSettings()
	}
	return info, err
}

func (a *App) SkipUpdate(version string) error {
	a.mu.Lock()
	a.settings.SkippedVersion = version
	a.mu.Unlock()
	return a.writeSettings()
}

// InstallUpdate downloads the installer for version, verifies its SHA-256
// against the release's SHA256SUMS.txt, runs it silently and quits. The
// installer closes this app and relaunches the new version.
// versionPattern is what a release version looks like ("0.2.24", "0.3.0-rc1").
// It's checked before the version goes into a download URL.
var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.]+)?$`)

func (a *App) InstallUpdate(version string) error {
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("not a version number: %q", version)
	}
	if !a.canInstallUpdates() {
		return errors.New("automatic install isn't available for this version; download it from the release page")
	}
	hc, _, err := a.updateClient(true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	base := releasesURL + "/download/v" + version + "/"

	want, err := signedChecksum(ctx, hc, base, "v"+version, installerAsset)
	if err != nil {
		return fmt.Errorf("couldn't verify the release: %w", err)
	}
	dir, err := os.MkdirTemp("", "sharkbite-update-")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, installerAsset)
	got, err := download(ctx, hc, base+installerAsset, path)
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("download failed: %w", err)
	}
	if !strings.EqualFold(got, want) {
		os.RemoveAll(dir)
		return errors.New("downloaded installer failed its checksum check; not installing")
	}
	if err := runInstaller(path); err != nil {
		return err
	}
	a.quit()
	return nil
}

// releasePub is the maintainer's release-signing public key (minisign). The
// private key is kept offline, never on GitHub, so a compromised GitHub
// account can't publish an update this app will install.
//
//go:embed release.pub
var releasePub []byte

// signedChecksum fetches the release's SHA256SUMS.txt and its signature,
// checks the signature against releasePub and that it was made for this tag
// (so an older signed release can't be passed off as a newer one), and
// returns the checksum listed for name.
func signedChecksum(ctx context.Context, hc *http.Client, base, tag, name string) (string, error) {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText(bytes.TrimSpace(releasePub)); err != nil {
		return "", fmt.Errorf("bad built-in key: %w", err)
	}
	sums, err := fetchSmall(ctx, hc, base+checksumsAsset)
	if err != nil {
		return "", fmt.Errorf("checksums: %w", err)
	}
	sig, err := fetchSmall(ctx, hc, base+signatureAsset)
	if err != nil {
		return "", fmt.Errorf("signature: %w", err)
	}
	return verifyChecksum(pub, sums, sig, tag, name)
}

func verifyChecksum(pub minisign.PublicKey, sums, sig []byte, tag, name string) (string, error) {
	if !minisign.Verify(pub, sums, sig) {
		return "", errors.New("the release's signature doesn't match; not installing it")
	}
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		return "", err
	}
	if s.TrustedComment != "SharkBiteTorrent "+tag {
		return "", fmt.Errorf("the signature is for %q, not %s", s.TrustedComment, tag)
	}
	return checksumFor(sums, name)
}

func fetchSmall(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// checksumFor finds name in sha256sum output ("<hex>  <name>"; binary mode
// prefixes the name with "*").
func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s not listed", name)
}

// download saves url to path and returns its SHA-256 hex digest.
func download(ctx context.Context, hc *http.Client, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 500<<20)); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canInstallUpdates reports whether "Update now" may install the official
// release. Not in nofunding builds: the official release has the funding
// links back in, so those users get a link to rebuild or download instead.
func (a *App) canInstallUpdates() bool {
	return fundingEnabled && !storeBuild && canSelfUpdate(a.exe)
}
