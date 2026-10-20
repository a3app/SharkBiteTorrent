package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	want := []byte(`{"privateKey":"super secret"}`)
	if err := WriteFile(path, want); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("super secret")) {
		t.Fatal("plaintext found in encrypted file")
	}
	if !encrypted(raw) {
		t.Fatal("missing header")
	}
	got, err := ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPlaintextUpgraded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	// A long, distinctive secret: short strings like "abc" can appear in the
	// base64 ciphertext by chance and make the check below flaky.
	plain := []byte(`{"token":"plaintext-token-must-not-survive"}`)
	os.WriteFile(path, plain, 0o600)
	got, err := ReadFile(path)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("got %q, %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if !encrypted(raw) || bytes.Contains(raw, []byte("plaintext-token")) {
		t.Fatal("file was not re-encrypted")
	}
	if got, err := ReadFile(path); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("after upgrade: %q, %v", got, err)
	}
}

func TestTamperedFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.json")
	if err := WriteFile(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	raw[len(raw)-5] ^= 1
	os.WriteFile(path, raw, 0o600)
	if _, err := ReadFile(path); err == nil {
		t.Fatal("expected error for tampered file")
	}
}

// encrypted: either header (keyring/DPAPI, or the key file without a keyring).
func encrypted(raw []byte) bool {
	return strings.HasPrefix(string(raw), header) || strings.HasPrefix(string(raw), fileHeader)
}
