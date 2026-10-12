//go:build !windows

package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
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

// masterKey loads the AES key from the OS keyring, creating it on first use.
func masterKey() ([]byte, error) {
	keyOnce.Do(func() {
		s, err := keyring.Get(keyringService, keyringUser)
		if errors.Is(err, keyring.ErrNotFound) {
			k := make([]byte, 32)
			if _, err = rand.Read(k); err != nil {
				keyErr = err
				return
			}
			if err = keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(k)); err != nil {
				keyErr = fmt.Errorf("saving key to the system keyring (is KWallet/GNOME Keyring running?): %w", err)
				return
			}
			key = k
			return
		}
		if err != nil {
			keyErr = fmt.Errorf("reading key from the system keyring (is KWallet/GNOME Keyring running?): %w", err)
			return
		}
		key, keyErr = base64.StdEncoding.DecodeString(s)
	})
	return key, keyErr
}

func aead() (cipher.AEAD, error) {
	k, err := masterKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func protect(plain []byte) ([]byte, error) {
	g, err := aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

func unprotect(c []byte) ([]byte, error) {
	g, err := aead()
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
