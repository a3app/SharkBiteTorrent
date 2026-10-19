package tunnel

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestListenUDP6WithoutIPv6(t *testing.T) {
	var tun Tunnel // no IPv6 address
	pc, err := tun.ListenUDP("udp6")
	if err != nil {
		t.Fatal(err) // the torrent library panics on this
	}
	if _, err := pc.WriteTo([]byte("x"), &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6969}); !errors.Is(err, errNoIPv6) {
		t.Errorf("write: %v", err)
	}
	pc.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, _, err := pc.ReadFrom(make([]byte, 10)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("read with deadline: %v", err)
	}
	pc.SetReadDeadline(time.Time{})
	done := make(chan error, 1)
	go func() { _, _, err := pc.ReadFrom(make([]byte, 10)); done <- err }()
	pc.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Errorf("read after close: %v", err)
	}
}
