//go:debug x509usefallbackroots=1

package main

// TLS certificates are checked by Go itself, against Mozilla's trusted roots
// bundled into the app (golang.org/x/crypto/x509roots/fallback), not by the
// operating system. macOS checks certificates in trustd and Windows in
// CryptoAPI, and either may fetch revocation data or missing intermediate
// certificates on its own, over the normal connection and in its own name:
// that would tell the network which sites the app talks to, even when the
// app's own traffic goes through the VPN. Go's checks never go online.
//
// The cost: certificate authorities added to the system by hand (a company
// proxy's, say) aren't trusted, and the roots are updated with the app.

import _ "golang.org/x/crypto/x509roots/fallback"
