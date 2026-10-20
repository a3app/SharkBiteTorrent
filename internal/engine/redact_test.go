package engine

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	for _, s := range []string{
		`announce to https://tracker.example.org/announce?passkey=abcdef0123456789abcdef failed`,
		`Get "http://t.example.net/0123456789abcdef0123456789abcdef/announce": EOF`,
		`udp://t.example.net:1337/announce/a1b2c3d4e5f6a7b8c9d0`,
		`restore: magnet:?xt=urn:btih:00&tr=https%3A%2F%2Ft%2Fannounce%3Fpasskey%3Dabcdef0123456789abcdef`,
	} {
		got := Redact(s)
		for _, secret := range []string{"abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef", "a1b2c3d4e5f6a7b8c9d0"} {
			if strings.Contains(got, secret) {
				t.Errorf("Redact(%q) = %q", s, got)
			}
		}
	}
	if got := Redact("https://tracker.example.org/announce"); got != "https://tracker.example.org/announce" {
		t.Errorf("plain URL changed: %q", got)
	}
}
