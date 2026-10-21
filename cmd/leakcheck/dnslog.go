//go:build linux

package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsLogger receives the watched processes' system DNS queries (nftables
// redirects those sent to the system's resolver here), records each name
// asked and the addresses answered, and passes the query on unchanged to the
// system's resolver. It runs in this process, outside the watched cgroup.
type dnsLogger struct {
	upstream string // ip:53
	udp      *net.UDPConn
	tcp      *net.TCPListener

	mu    sync.Mutex
	names map[string]map[string]bool // name asked → addresses answered
	udpQs int64                      // UDP queries received
}

// systemNameservers are the IPv4 nameservers in /etc/resolv.conf (glibc
// can't send queries to anything else). IPv6 ones aren't redirected, so
// queries to them count as unchecked DNS.
func systemNameservers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			if ip := net.ParseIP(fields[1]); ip != nil && ip.To4() != nil {
				out = append(out, ip.String())
			}
		}
	}
	return out
}

func startDNSLogger(upstream string) (*dnsLogger, error) {
	l := &dnsLogger{upstream: net.JoinHostPort(upstream, "53"), names: map[string]map[string]bool{}}
	var err error
	if l.udp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
		return nil, err
	}
	if l.tcp, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
		l.udp.Close()
		return nil, err
	}
	go l.serveUDP()
	go l.serveTCP()
	return l, nil
}

func (l *dnsLogger) ports() (udp, tcp int) {
	return l.udp.LocalAddr().(*net.UDPAddr).Port, l.tcp.Addr().(*net.TCPAddr).Port
}

func (l *dnsLogger) close() {
	l.udp.Close()
	l.tcp.Close()
}

func (l *dnsLogger) serveUDP() {
	buf := make([]byte, 65535)
	for {
		n, from, err := l.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		q := append([]byte(nil), buf[:n]...)
		l.mu.Lock()
		l.udpQs++
		l.mu.Unlock()
		go func() {
			l.record(q, nil) // asked, even if no answer comes
			c, err := net.DialTimeout("udp", l.upstream, 5*time.Second)
			if err != nil {
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := c.Write(q); err != nil {
				return
			}
			reply := make([]byte, 65535)
			m, err := c.Read(reply)
			if err != nil {
				return
			}
			l.record(q, reply[:m])
			l.udp.WriteToUDP(reply[:m], from)
		}()
	}
}

func (l *dnsLogger) serveTCP() {
	for {
		c, err := l.tcp.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			up, err := net.DialTimeout("tcp", l.upstream, 5*time.Second)
			if err != nil {
				return
			}
			defer up.Close()
			for {
				c.SetDeadline(time.Now().Add(10 * time.Second))
				up.SetDeadline(time.Now().Add(10 * time.Second))
				q, err := readTCPMsg(c)
				if err != nil {
					return
				}
				l.record(q, nil)
				if writeTCPMsg(up, q) != nil {
					return
				}
				reply, err := readTCPMsg(up)
				if err != nil {
					return
				}
				l.record(q, reply)
				if writeTCPMsg(c, reply) != nil {
					return
				}
			}
		}()
	}
}

func readTCPMsg(r io.Reader) ([]byte, error) {
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeTCPMsg(w io.Writer, b []byte) error {
	if len(b) > 65535 {
		return errors.New("too long")
	}
	_, err := w.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(b))), b...))
	return err
}

// record notes the names asked in query and the addresses in reply.
func (l *dnsLogger) record(query, reply []byte) {
	var p dnsmessage.Parser
	if _, err := p.Start(query); err != nil {
		return
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, q := range qs {
		name := dnsName(q.Name)
		if l.names[name] == nil {
			l.names[name] = map[string]bool{}
		}
	}
	if reply == nil {
		return
	}
	// Every address in the answer counts for the name asked: a CNAME
	// chain's addresses are listed under the last alias.
	for a := range answers(reply) {
		for _, q := range qs {
			l.names[dnsName(q.Name)][a] = true
		}
	}
}

// answers returns the A and AAAA addresses in reply's answer section.
func answers(reply []byte) map[string]bool {
	var p dnsmessage.Parser
	if _, err := p.Start(reply); err != nil {
		return nil
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil
	}
	out := map[string]bool{}
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return out
		}
		switch h.Type {
		case dnsmessage.TypeA:
			if r, err := p.AResource(); err == nil {
				out[net.IP(r.A[:]).String()] = true
			}
		case dnsmessage.TypeAAAA:
			if r, err := p.AAAAResource(); err == nil {
				out[net.IP(r.AAAA[:]).String()] = true
			}
		default:
			if p.SkipAnswer() != nil {
				return out
			}
		}
	}
}

func dnsName(n dnsmessage.Name) string {
	return strings.ToLower(strings.TrimSuffix(n.String(), "."))
}

// lookups returns the names asked, with the addresses answered for each.
func (l *dnsLogger) lookups() map[string][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string][]string{}
	for name, addrs := range l.names {
		list := []string{}
		for a := range addrs {
			list = append(list, a)
		}
		out[name] = list
	}
	return out
}

// udpQueries is how many UDP queries reached the recorder.
func (l *dnsLogger) udpQueries() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.udpQs
}
