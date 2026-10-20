//go:build !windows

package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	keyringKey = func() ([]byte, error) { return nil, errNoKeyring }
	oldPresent := keyringPresent
	t.Cleanup(func() { keyringPresent = oldPresent })
	keyringPresent = func() bool { return false }
	keyMu.Lock()
	cached := key
	key = nil // an earlier test may have loaded the real keyring's key
	keyMu.Unlock()
	t.Cleanup(func() { keyMu.Lock(); key = cached; keyMu.Unlock() })

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
	// Reading it moved it to the keyring.
	if raw, _ := os.ReadFile(path); !strings.HasPrefix(string(raw), header) {
		t.Error("key-file secret wasn't moved to the keyring when read")
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

// A keyring that exists but fails (locked, still starting, access refused)
// never makes secrets fall back to the key file, and doesn't count as missing.
func TestKeyringErrorNoFallback(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "secret.key")
	oldPath, oldKeyring := keyFilePath, keyringKey
	t.Cleanup(func() {
		keyFilePath, keyringKey = oldPath, oldKeyring
		fileKeyV = nil
	})
	keyFilePath = func() (string, error) { return keyPath, nil }
	fileKeyV = nil
	keyringKey = func() ([]byte, error) { return nil, errors.New("keyring locked") }
	oldPresent := keyringPresent
	t.Cleanup(func() { keyringPresent = oldPresent })
	keyringPresent = func() bool { return true }

	if KeyringMissing() {
		t.Error("a locked keyring counted as missing")
	}
	if err := WriteFile(filepath.Join(t.TempDir(), "s.json"), []byte("x")); err == nil {
		t.Error("saved without the keyring")
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("key file created: %v", err)
	}
}

// A keyring error is retried after keyRetry, not kept for the whole session.
func TestKeyringRetry(t *testing.T) {
	oldLoad := loadKeyringKey
	t.Cleanup(func() {
		loadKeyringKey = oldLoad
		keyMu.Lock()
		key, keyErr, keyErrAt = nil, nil, time.Time{}
		keyMu.Unlock()
	})
	keyMu.Lock()
	key, keyErr = nil, nil
	keyMu.Unlock()
	calls := 0
	loadKeyringKey = func() ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("still starting")
		}
		return bytes.Repeat([]byte{1}, 32), nil
	}
	if _, err := keyringKey(); err == nil {
		t.Fatal("first call should fail")
	}
	if _, err := keyringKey(); err == nil || calls != 1 {
		t.Fatalf("error not reused within keyRetry (calls %d)", calls)
	}
	keyMu.Lock()
	keyErrAt = time.Now().Add(-keyRetry - time.Second)
	keyMu.Unlock()
	if k, err := keyringKey(); err != nil || len(k) != 32 || calls != 2 {
		t.Fatalf("not retried: %v (calls %d)", err, calls)
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
