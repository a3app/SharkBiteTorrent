package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Support and sign-up links shown in the app. This is the one place to edit
// them. Referral links are disclosed in the UI ("supports development"), and
// providers are never ordered or recommended by commission.
//
// Building with `-tags nofunding` removes all of it (see links_nofunding.go):
// no sponsor button, plain provider links (plainProviderURLs), no referral
// labels, and no in-app installs of the official (funded) release.
var links = Links{
	Repo: "https://github.com/trevercomb/SharkBiteTorrent",
	// Sponsor shows as "Support development" in Settings → About.
	Sponsor: "https://github.com/sponsors/trevercomb",
	Providers: map[string]ProviderLink{
		// Affiliate programs: replace the URL with your referral link and set
		// Referral to true.
		"nordvpn": {URL: "https://nordvpn.com/", Referral: false},
		"pia":     {URL: "https://www.privateinternetaccess.com/", Referral: false},
		// No affiliate programs (by the providers' choice): plain links.
		"mullvad": {URL: "https://mullvad.net/"},
		"ivpn":    {URL: "https://www.ivpn.net/"},
	},
}

// plainProviderURLs are the providers' ordinary homepages, used for every
// provider in nofunding builds.
var plainProviderURLs = map[string]string{
	"nordvpn": "https://nordvpn.com/",
	"pia":     "https://www.privateinternetaccess.com/",
	"mullvad": "https://mullvad.net/",
	"ivpn":    "https://www.ivpn.net/",
}

type Links struct {
	Repo      string                  `json:"repo"`
	Sponsor   string                  `json:"sponsor"`
	Providers map[string]ProviderLink `json:"providers"`
}

type ProviderLink struct {
	URL      string `json:"url"`
	Referral bool   `json:"referral"` // a commission supports development
}

//go:embed wails.json
var wailsJSON []byte

type About struct {
	Version string `json:"version"`
	License string `json:"license"`
	Links   Links  `json:"links"`
	// Funding is false in builds made with -tags nofunding.
	Funding bool `json:"funding"`
	// Store is true in the Microsoft Store build (-tags store).
	Store bool `json:"store"`
}

// GetAbout returns the version (from wails.json, which the release workflow
// stamps from the git tag) and the support/sign-up links.
func (a *App) GetAbout() About {
	var w struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	json.Unmarshal(wailsJSON, &w)
	return About{Version: w.Info.ProductVersion, License: "GPL-3.0-or-later", Links: links, Funding: fundingEnabled, Store: storeBuild}
}

// OpenURL opens an http(s) link in the user's default browser.
func (a *App) OpenURL(link string) error {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("not a web link")
	}
	runtime.BrowserOpenURL(a.ctx, link)
	return nil
}
