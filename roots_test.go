package main

import (
	"crypto/x509"
	"runtime/metrics"
	"testing"
)

// The app's certificate checks use the bundled roots, not the system's
// verifier (see roots.go). Go counts each time the setting overrides the
// system's roots.
func TestFallbackRootsInUse(t *testing.T) {
	if _, err := x509.SystemCertPool(); err != nil {
		t.Fatal(err)
	}
	s := []metrics.Sample{{Name: "/godebug/non-default-behavior/x509usefallbackroots:events"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[0].Value.Uint64() == 0 {
		t.Error("the system's certificate roots are used, not the bundled ones")
	}
}
