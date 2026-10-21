package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"sync"
	"time"
)

// The torrent library's HTTP tracker client turns certificate checks off
// (InsecureSkipVerify), so anyone between the VPN's exit and an HTTPS tracker
// could read the passkey in a private tracker's announce URL. It dials
// through TrackerDialContext, though, which is ours: verifiedTrackerDial
// spots a TLS handshake on the connection, makes its own properly verified
// TLS connection to the tracker, and lets the library's unverified handshake
// happen only in memory, against a throwaway certificate of the app's own.
// Plain HTTP trackers pass straight through.

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// trackerRoots verifies trackers' certificates; nil means the roots built
// into the app (roots.go in package main: Go's own checks, not the OS's).
// A var so tests can trust their own server.
var trackerRoots *x509.CertPool

func verifiedTrackerDial(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &sniffConn{raw: raw, inner: raw, addr: addr}, nil
	}
}

// sniffConn looks at the library's first write: a TLS handshake record
// (0x16) switches the connection to the verifying bridge.
type sniffConn struct {
	raw  net.Conn
	addr string

	mu      sync.Mutex
	inner   net.Conn // raw, or the library's end of the in-memory bridge
	decided bool
}

func (c *sniffConn) conn() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inner
}

func (c *sniffConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	if !c.decided {
		c.decided = true
		if len(b) > 0 && b[0] == 0x16 {
			lib, ours := net.Pipe()
			c.inner = lib
			go bridge(ours, c.raw, c.addr)
		}
	}
	inner := c.inner
	c.mu.Unlock()
	return inner.Write(b)
}

func (c *sniffConn) Read(b []byte) (int, error) { return c.conn().Read(b) }

func (c *sniffConn) Close() error {
	err := c.conn().Close()
	c.raw.Close()
	return err
}

func (c *sniffConn) LocalAddr() net.Addr  { return c.raw.LocalAddr() }
func (c *sniffConn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }
func (c *sniffConn) SetDeadline(t time.Time) error {
	c.raw.SetDeadline(t)
	return c.conn().SetDeadline(t)
}
func (c *sniffConn) SetReadDeadline(t time.Time) error {
	c.raw.SetReadDeadline(t)
	return c.conn().SetReadDeadline(t)
}
func (c *sniffConn) SetWriteDeadline(t time.Time) error {
	c.raw.SetWriteDeadline(t)
	return c.conn().SetWriteDeadline(t)
}

// bridge answers the library's TLS handshake on ours, but only after a
// verified TLS connection to the tracker (for the name the library asked
// for) has succeeded; then it copies data both ways.
func bridge(ours, raw net.Conn, addr string) {
	defer ours.Close()
	defer raw.Close()
	var up *tls.Conn
	cert, err := bridgeCert()
	if err != nil {
		return
	}
	srv := tls.Server(ours, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			name := hello.ServerName
			if name == "" {
				name, _, _ = net.SplitHostPort(addr)
			}
			up = tls.Client(raw, &tls.Config{ServerName: name, RootCAs: trackerRoots, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := up.HandshakeContext(ctx); err != nil {
				return nil, errors.New("tracker certificate not trusted") // the library sees a failed handshake
			}
			return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
		},
	})
	if err := srv.Handshake(); err != nil || up == nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, srv); up.CloseWrite(); done <- struct{}{} }()
	go func() { io.Copy(srv, up); srv.CloseWrite(); done <- struct{}{} }()
	<-done
	<-done
}

var (
	certOnce sync.Once
	certV    tls.Certificate
	certErr  error
)

// bridgeCert is the throwaway certificate for the in-memory handshake.
func bridgeCert() (tls.Certificate, error) {
	certOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			certErr = err
			return
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "Shark BiteTorrent tracker bridge"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			certErr = err
			return
		}
		certV = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	})
	return certV, certErr
}
