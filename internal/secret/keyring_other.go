//go:build !windows && !linux

package secret

// keyringExists: macOS always has the Keychain, so a Keychain error (a
// denied prompt, say) never falls back to the key file.
func keyringExists() bool { return true }
