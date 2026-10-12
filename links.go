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
var links = Links{
	Repo: "https://github.com/trevercomb/torrent-client",
	// Sponsor is hidden in the app until set, e.g.
	// "https://github.com/sponsors/trevercomb".
	Sponsor: "",
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
	return About{Version: w.Info.ProductVersion, License: "GPL-3.0-or-later", Links: links}
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
