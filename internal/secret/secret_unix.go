//go:build !windows

package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "SharkBiteTorrent"
	keyringUser    = "file-encryption-key"
)

var (
	keyOnce sync.Once
	key     []byte
	keyErr  error
)

// keyringKey loads the AES key from the OS keyring, creating it on first
// use. A var so tests can stand in for a missing keyring.
var keyringKey = func() ([]byte, error) {
	keyOnce.Do(func() {
		s, err := keyring.Get(keyringService, keyringUser)
		if errors.Is(err, keyring.ErrNotFound) {
			k := make([]byte, 32)
			if _, err = rand.Read(k); err != nil {
				keyErr = err
				return
			}
			if err = keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(k)); err != nil {
				keyErr = fmt.Errorf("saving key to the system keyring: %w", err)
				return
			}
			key = k
			return
		}
		if err != nil {
			keyErr = fmt.Errorf("reading key from the system keyring: %w", err)
			return
		}
		key, keyErr = base64.StdEncoding.DecodeString(s)
	})
	return key, keyErr
}

// KeyringMissing reports that there's no usable keyring, so new secrets are
// protected by the key file's permissions alone (shown in Settings).
func KeyringMissing() bool {
	_, err := keyringKey()
	return err != nil
}

var (
	fileKeyMu sync.Mutex
	fileKeyV  []byte
)

// keyFilePath is the fallback key, ~/.config/SharkBiteTorrent/secret.key (or
// the macOS equivalent), readable only by this user.
var keyFilePath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SharkBiteTorrent", "secret.key"), nil
}

// fileKey loads the key file, creating it on first use. Like Chrome's and VS
// Code's "basic" store on Linux: anyone who can read the user's files can
// read it, but secrets aren't sitting in plain text.
func fileKey() ([]byte, error) {
	fileKeyMu.Lock()
	defer fileKeyMu.Unlock()
	if fileKeyV != nil {
		return fileKeyV, nil
	}
	p, err := keyFilePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if b, err = createKeyFile(p); errors.Is(err, fs.ErrExist) {
			b, err = os.ReadFile(p) // another process made it meanwhile
		}
	}
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(p, 0o600) // keep it private even if someone loosened it
	}
	k, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("%s is damaged", p)
	}
	fileKeyV = k
	return k, nil
}

// createKeyFile writes a new random key; fs.ErrExist if the file appeared.
func createKeyFile(p string) ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	enc := []byte(base64.StdEncoding.EncodeToString(k))
	_, werr := f.Write(enc)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(p)
		return nil, werr
	}
	return enc, nil
}

func aead(k []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// protect encrypts with the keyring key, or the key file if there's no
// keyring; keyFile says which, so the file gets the matching header.
func protect(plain []byte) ([]byte, bool, error) {
	k, err := keyringKey()
	keyFile := false
	if err != nil {
		if k, err = fileKey(); err != nil {
			return nil, false, fmt.Errorf("no system keyring and no key file: %w", err)
		}
		keyFile = true
	}
	g, err := aead(k)
	if err != nil {
		return nil, false, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, false, err
	}
	return g.Seal(nonce, nonce, plain, nil), keyFile, nil
}

// unprotect decrypts with the key the file's header names. A keyring file
// never falls back to the key file (or the other way round): a keyring
// that's slow to start must not leave older files unreadable.
func unprotect(c []byte, keyFile bool) ([]byte, error) {
	var k []byte
	var err error
	if keyFile {
		k, err = fileKey()
	} else if k, err = keyringKey(); err != nil {
		err = fmt.Errorf("%w (is KWallet/GNOME Keyring running?)", err)
	}
	if err != nil {
		return nil, err
	}
	g, err := aead(k)
	if err != nil {
		return nil, err
	}
	if len(c) < g.NonceSize() {
		return nil, errors.New("decrypt: data too short")
	}
	out, err := g.Open(nil, c[:g.NonceSize()], c[g.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt (was this file created by another user?): %w", err)
	}
	return out, nil
}
