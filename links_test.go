package main

import (
	"net/url"
	"testing"
)

// Runs in both build modes:
//
//	go test -run TestLinks .
//	go test -tags nofunding -run TestLinks .
func TestLinks(t *testing.T) {
	a := (&App{}).GetAbout()
	if a.Funding != fundingEnabled {
		t.Fatalf("About.Funding = %v, want %v", a.Funding, fundingEnabled)
	}
	for id := range plainProviderURLs {
		p, ok := a.Links.Providers[id]
		if !ok {
			t.Errorf("provider %q has no link", id)
			continue
		}
		if u, err := url.Parse(p.URL); err != nil || u.Scheme != "https" {
			t.Errorf("provider %q: bad URL %q", id, p.URL)
		}
		if !fundingEnabled && (p.Referral || p.URL != plainProviderURLs[id]) {
			t.Errorf("nofunding build still has a referral link for %q: %+v", id, p)
		}
	}
	if !fundingEnabled && a.Links.Sponsor != "" {
		t.Error("nofunding build still has a sponsor link")
	}
}
