// Package secret encrypts small files at rest so that opening them shows only
// ciphertext and a copy taken to another user account or machine can't be
// decrypted. The key never touches disk in usable form:
//
//   - Windows: DPAPI, scoped to the current Windows user.
//   - macOS/Linux: a random 256-bit key kept in the OS keyring (Keychain, or
//     the Secret Service: KWallet/GNOME Keyring), used with AES-256-GCM.
//     Linux desktops without a keyring (Hyprland, Sway, i3, …) fall back to
//     a key file readable only by the user (see secret_unix.go); files
//     encrypted with it carry their own header.
//
// Code running as the same logged-in user can still decrypt; that's inherent
// to any scheme that doesn't ask the user for a master password.
package secret

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// header marks encrypted files, so older plaintext files can be detected and
// upgraded transparently.
const (
	header     = "SBT-SECRET-v1\n"         // DPAPI / keyring key
	fileHeader = "SBT-SECRET-v1-keyfile\n" // key file (no keyring)
)

// pathLocks serializes writes to the same file (a migration in ReadFile and
// a save can otherwise interleave).
var pathLocks sync.Map // path → *sync.Mutex

func lockPath(path string) func() {
	m, _ := pathLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// WriteFile encrypts data and writes it atomically.
func WriteFile(path string, data []byte) error {
	defer lockPath(path)()
	return writeFile(path, data)
}

// writeFile is WriteFile with the path's lock held.
func writeFile(path string, data []byte) error {
	enc, keyFile, err := protect(data)
	if err != nil {
		return err
	}
	h := header
	if keyFile {
		h = fileHeader
	}
	out := append([]byte(h), base64.StdEncoding.EncodeToString(enc)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// A temporary file of its own (0600), so concurrent writers never share one.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(out)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return werr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}

// ReadFile returns the decrypted contents. A file still in plaintext (from
// before encryption was added) is returned as-is and re-written encrypted.
func ReadFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	h, keyFile := header, false
	if bytes.HasPrefix(raw, []byte(fileHeader)) {
		h, keyFile = fileHeader, true
	} else if !bytes.HasPrefix(raw, []byte(header)) {
		if err := WriteFile(path, raw); err != nil {
			return nil, fmt.Errorf("encrypting %s: %w", filepath.Base(path), err)
		}
		return raw, nil
	}
	enc, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(raw[len(h):])))
	if err != nil {
		return nil, fmt.Errorf("%s is corrupted: %w", filepath.Base(path), err)
	}
	plain, err := unprotect(enc, keyFile)
	if err != nil {
		return nil, err
	}
	// Saved with the key file while there was no keyring: now that there is
	// one, move it there (best effort; it stays readable either way).
	if keyFile && keyringUsable() {
		migrate(path, raw, plain)
	}
	return plain, nil
}

// migrate re-saves a key-file secret with the keyring, unless the file
// changed since it was read (a newer save must not be overwritten).
func migrate(path string, raw, plain []byte) {
	defer lockPath(path)()
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, raw) {
		return
	}
	writeFile(path, plain)
}
