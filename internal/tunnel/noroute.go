package tunnel

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

var errNoIPv6 = errors.New("tunnel has no IPv6 address")

// noRoutePacketConn stands in for an IPv6 UDP socket on a tunnel that has no
// IPv6: every send fails, reads wait until Close (or a deadline). It has no
// OS socket, so nothing can leave through it.
type noRoutePacketConn struct {
	once   sync.Once
	closed chan struct{}
	mu     sync.Mutex
	dl     time.Time
}

func newNoRoutePacketConn() *noRoutePacketConn {
	return &noRoutePacketConn{closed: make(chan struct{})}
}

func (c *noRoutePacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	c.mu.Lock()
	dl := c.dl
	c.mu.Unlock()
	var timeout <-chan time.Time
	if !dl.IsZero() {
		t := time.NewTimer(time.Until(dl))
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-timeout:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *noRoutePacketConn) WriteTo([]byte, net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
		return 0, errNoIPv6
	}
}

func (c *noRoutePacketConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *noRoutePacketConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Unspecified(), 0))
}

func (c *noRoutePacketConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

// SetReadDeadline applies to reads that start after it's set, which is how
// the tracker client uses it.
func (c *noRoutePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.dl = t
	c.mu.Unlock()
	return nil
}

func (c *noRoutePacketConn) SetWriteDeadline(time.Time) error { return nil }
