//go:build nofunding

package main

// Built with -tags nofunding: strip every funding link at startup.
const fundingEnabled = false

func init() {
	links.Sponsor = ""
	for id := range links.Providers {
		links.Providers[id] = ProviderLink{URL: plainProviderURLs[id]}
	}
}
