package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	"aead.dev/minisign"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.1", "0.2.0", true},
		{"0.2.10", "0.2.9", true},
		{"0.3", "0.2.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.2.1", false},
		{"0.2", "0.2.0", false},
		{"0.3.0", "0.3.0-rc1", true},
		{"0.3.0-rc1", "0.3.0", false},
		{"0.3.0-rc2", "0.3.0-rc1", true},
		{"0.3.0-rc1", "0.2.18", true},
		{"0.2.18", "0.3.0-rc1", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestVersionPattern(t *testing.T) {
	for _, v := range []string{"0.2.24", "1.0", "0.3.0-rc1", "1.2.3.4"} {
		if !versionPattern.MatchString(v) {
			t.Errorf("rejected %q", v)
		}
	}
	for _, v := range []string{"", "1", "../../x", "0.2.24/../../evil", "1.2.3?x=1", "v1.2.3", "1.2.3 "} {
		if versionPattern.MatchString(v) {
			t.Errorf("accepted %q", v)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sums := []byte("aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111  SharkBiteTorrent.exe\n" +
		"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222  SharkBiteTorrent-amd64-installer.exe\n")
	sig := minisign.SignWithComments(priv, sums, "SharkBiteTorrent v0.3.0", "")

	got, err := verifyChecksum(pub, sums, sig, "v0.3.0", installerAsset)
	if err != nil || got != "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := verifyChecksum(pub, sums, sig, "v0.3.0", "missing.exe"); err == nil {
		t.Error("unlisted file accepted")
	}
	// An older genuine release re-uploaded under a newer tag.
	if _, err := verifyChecksum(pub, sums, sig, "v0.4.0", installerAsset); err == nil {
		t.Error("signature for another version accepted")
	}
	// Tampered checksums.
	bad := bytes.Replace(sums, []byte("bbbb"), []byte("cccc"), 1)
	if _, err := verifyChecksum(pub, bad, sig, "v0.3.0", installerAsset); err == nil {
		t.Error("tampered checksums accepted")
	}
	// Signed by someone else.
	_, other, _ := minisign.GenerateKey(rand.Reader)
	if _, err := verifyChecksum(pub, sums, minisign.SignWithComments(other, sums, "SharkBiteTorrent v0.3.0", ""), "v0.3.0", installerAsset); err == nil {
		t.Error("signature from another key accepted")
	}
}

func TestReleaseKeyEmbedded(t *testing.T) {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText(bytes.TrimSpace(releasePub)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%X", pub.ID()) != "F59273E544BB8BA3" {
		t.Errorf("embedded key %X", pub.ID())
	}
}
