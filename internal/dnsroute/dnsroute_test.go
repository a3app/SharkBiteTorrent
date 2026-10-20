package dnsroute

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestModes(t *testing.T) {
	t.Cleanup(UseSystem)
	var tunneled atomic.Bool // A and AAAA lookups run at once
	UseTunnel(func(context.Context, string) (net.Conn, error) {
		tunneled.Store(true)
		return nil, errors.New("test tunnel")
	})
	net.DefaultResolver.LookupHost(context.Background(), "tracker.example.org")
	if !tunneled.Load() {
		t.Error("tunnel mode didn't use the tunnel")
	}

	UseNone()
	if _, err := dial(context.Background(), "udp", "192.0.2.1:53"); !errors.Is(err, errNone) {
		t.Errorf("none mode dialed: %v", err)
	}
	// Only lookups marked WithSystem get through.
	if _, err := dial(WithSystem(context.Background()), "udp", "192.0.2.1:53"); errors.Is(err, errNone) {
		t.Error("WithSystem didn't allow")
	}
}

// Queries with a local search domain appended never reach the VPN's DNS.
func TestSearchFilter(t *testing.T) {
	if got := parseSearch("nameserver 1.1.1.1\nsearch corp.local. home.arpa\ndomain lan\n"); len(got) != 3 || got[0] != "corp.local" || got[2] != "lan" {
		t.Fatalf("parseSearch: %v", got)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	f := &searchFilter{Conn: client, search: []string{"corp.local"}}
	query := func(name string) []byte {
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
		b.StartQuestions()
		b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
		msg, _ := b.Finish()
		return msg
	}
	if _, err := f.Write(query("tracker.example.org.corp.local.")); !errors.Is(err, errSearchDomain) {
		t.Errorf("search-domain query sent: %v", err)
	}
	go func() { buf := make([]byte, 512); server.Read(buf) }()
	if _, err := f.Write(query("tracker.example.org.")); err != nil {
		t.Errorf("normal query refused: %v", err)
	}
}
