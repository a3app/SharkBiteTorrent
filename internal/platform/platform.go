// Package platform hides OS-specific desktop integration: opening folders and
// registering as the handler for magnet: links and .torrent files.
package platform

type AssocStatus struct {
	Supported bool `json:"supported"` // false where the OS handles it (macOS app bundle)
	Magnet    bool `json:"magnet"`    // magnet links open in this app
	Torrent   bool `json:"torrent"`   // .torrent files open in this app
	// CanOpenSettings is true if OpenDefaultAppsSettings does something useful.
	CanOpenSettings bool `json:"canOpenSettings"`
}
