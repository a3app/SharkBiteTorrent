package secret

import (
	"bytes"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// entropy is mixed into the encryption so other apps using DPAPI for the same
// user can't trivially decrypt our blobs by calling CryptUnprotectData.
var entropy = []byte("SharkBiteTorrent secret store")

// KeyringMissing is for Settings: Windows always has DPAPI.
func KeyringMissing() bool { return false }

func protect(plain []byte) ([]byte, bool, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, false, fmt.Errorf("encrypt: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return bytes.Clone(unsafe.Slice(out.Data, out.Size)), false, nil
}

func unprotect(cipher []byte, keyFile bool) ([]byte, error) {
	if keyFile {
		return nil, errors.New("decrypt: this file was encrypted on Linux or macOS")
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(cipher), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("decrypt (was this file created by another Windows user?): %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return bytes.Clone(unsafe.Slice(out.Data, out.Size)), nil
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// UseKeyFileForTests: DPAPI works unattended, so tests use it as is.
func UseKeyFileForTests(string) {}

// keyringUsable: Windows never uses the key file.
func keyringUsable() bool { return false }
