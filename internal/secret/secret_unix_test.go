//go:build !windows

package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without a keyring, secrets go into files encrypted with a private key file,
// marked as such; they stay readable when the keyring comes back.
func TestKeyFileFallback(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "conf", "secret.key")
	oldPath, oldKeyring := keyFilePath, keyringKey // restored for the other tests
	t.Cleanup(func() {
		keyFilePath, keyringKey = oldPath, oldKeyring
		fileKeyV = nil
	})
	keyFilePath = func() (string, error) { return keyPath, nil }
	fileKeyV = nil
	keyringKey = func() ([]byte, error) { return nil, errors.New("no keyring") }

	if !KeyringMissing() {
		t.Error("KeyringMissing false without a keyring")
	}
	path := filepath.Join(t.TempDir(), "s.json")
	want := []byte(`{"privateKey":"secret without a keyring"}`)
	if err := WriteFile(path, want); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), fileHeader) || bytes.Contains(raw, []byte("without a keyring")) {
		t.Fatalf("not encrypted with the key file: %q", raw)
	}
	fi, err := os.Stat(keyPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}

	// The keyring shows up later: the key-file secret is still readable,
	// and new secrets use the keyring.
	fileKeyV = nil // as after a restart
	keyringKey = func() ([]byte, error) { return bytes.Repeat([]byte{7}, 32), nil }
	if got, err := ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("after the keyring came back: %q, %v", got, err)
	}
	if err := WriteFile(path, want); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); !strings.HasPrefix(string(raw), header) {
		t.Error("new secret didn't use the keyring")
	}

	// A keyring secret is never "decrypted" with the key file instead.
	keyringKey = func() ([]byte, error) { return nil, errors.New("keyring locked") }
	if _, err := ReadFile(path); err == nil {
		t.Error("keyring file read without the keyring")
	}
}

// Tests never touch the real key file in the user's config folder.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "secret-test")
	keyFilePath = func() (string, error) { return filepath.Join(dir, "secret.key"), nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
