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
)

// header marks encrypted files, so older plaintext files can be detected and
// upgraded transparently.
const (
	header     = "SBT-SECRET-v1\n"         // DPAPI / keyring key
	fileHeader = "SBT-SECRET-v1-keyfile\n" // key file (no keyring)
)

// WriteFile encrypts data and writes it atomically.
func WriteFile(path string, data []byte) error {
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
		WriteFile(path, plain)
	}
	return plain, nil
}
