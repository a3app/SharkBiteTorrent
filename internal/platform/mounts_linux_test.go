package platform

import "testing"

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/mnt/my\040nas`); got != "/mnt/my nas" {
		t.Errorf("got %q", got)
	}
	if got := unescapeMount(`/mnt/back\slash`); got != `/mnt/back\slash` {
		t.Errorf("got %q", got)
	}
}
