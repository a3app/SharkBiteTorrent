// Package vpnprov logs into commercial VPN providers and turns an account
// into a WireGuard tunnel config, so users don't have to download .conf files.
// Only providers with an official (or officially sanctioned) API are included.
package vpnprov

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"

	"sharkbite/internal/tunnel"
)

// Field is a credential input shown in the login form.
type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "text" or "password"
	Help  string `json:"help,omitempty"`
}

type Info struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// PortForwarding is true if the provider can forward a port to us, which
	// lets other peers connect (better seeding).
	PortForwarding bool   `json:"portForwarding"`
	Note           string `json:"note,omitempty"`
}

type Region struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PortForward bool   `json:"portForward"`
}

// Account is what a successful login produces. Data holds provider-specific
// state (keys, tokens, passwords); the app encrypts it at rest.
type Account struct {
	Provider string            `json:"provider"`
	Display  string            `json:"display"` // e.g. masked account number
	Data     map[string]string `json:"data"`
}

// Connection is everything needed to bring up the tunnel to one server.
type Connection struct {
	Config tunnel.Config
	Server string // human-readable server name for the UI
	// PortForward, if set, runs once the tunnel is up and returns the port
	// forwarded to us, plus a keepalive to call every `every`.
	PortForward func(ctx context.Context, t *tunnel.Tunnel) (port int, keepalive func(context.Context) error, every time.Duration, err error)
}

type Provider interface {
	Info() Info
	// Login validates credentials and registers a WireGuard key if needed.
	Login(ctx context.Context, creds map[string]string) (*Account, error)
	// Logout releases server-side resources (sessions, keys) where possible.
	Logout(ctx context.Context, acc *Account) error
	Regions(ctx context.Context) ([]Region, error)
	// Connect picks a server in region ("" = automatic; homeCountry is the
	// user's ISO country code, used for automatic choice) and returns a config.
	Connect(ctx context.Context, acc *Account, region, homeCountry string) (*Connection, error)
}

// NeedFieldError means the login needs one more input (e.g. a 2FA code).
// The UI should show Field and retry Login with it filled in.
type NeedFieldError struct {
	Field   Field
	Message string
}

func (e *NeedFieldError) Error() string { return e.Message }

var registry = map[string]Provider{}

func register(p Provider) { registry[p.Info().ID] = p }

func Get(id string) (Provider, bool) {
	p, ok := registry[id]
	return p, ok
}

func All() []Info {
	var out []Info
	for _, p := range registry {
		out = append(out, p.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- helpers shared by providers ---

var (
	tunnelMu   sync.Mutex
	tunnelDial func(ctx context.Context, network, addr string) (net.Conn, error)
)

// SetTunnel makes provider API calls (server lists, sign-in, sign-out) go
// through the running VPN tunnel; nil goes back to the normal connection.
// The app sets it whenever a tunnel is up, so these calls only go out
// directly while no tunnel exists, i.e. when connecting to a provider.
func SetTunnel(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	tunnelMu.Lock()
	tunnelDial = dial
	tunnelMu.Unlock()
}

// apiClient talks to provider APIs: through the tunnel if one is up (see
// SetTunnel), otherwise over the normal connection with its own resolver
// (the torrent engine points net.DefaultResolver into the tunnel). Each
// request gets a fresh connection that's closed afterwards, so nothing
// lingers.
func apiClient() *http.Client {
	tunnelMu.Lock()
	dial := tunnelDial
	tunnelMu.Unlock()
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second, Resolver: &net.Resolver{}}).DialContext
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DialContext:         dial,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
	}
}

// httpClient is overridable in tests.
var httpClient = apiClient

func newKeypair() (priv, pub string, err error) {
	var k [32]byte
	if _, err = rand.Read(k[:]); err != nil {
		return
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p), nil
}

// ErrRenew from a port-forward keepalive means the mapping can't be extended
// and the connection should be re-established to get a new one.
var ErrRenew = errors.New("port forwarding needs a fresh connection")

// publicKey derives the WireGuard public key from a base64 private key.
func publicKey(priv string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(k) != 32 {
		return "", errors.New("bad private key")
	}
	p, err := curve25519.X25519(k, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(p), nil
}

// getJSON GETs url and decodes the JSON body into v.
func getJSON(ctx context.Context, hc *http.Client, url string, v any, setup ...func(*http.Request)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for _, f := range setup {
		f(req)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		return &httpError{resp.StatusCode, strings.TrimSpace(string(body))}
	}
	return json.Unmarshal(body, v)
}

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	if len(e.Body) > 200 {
		e.Body = e.Body[:200]
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

func statusOf(err error) int {
	var he *httpError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func mask(s string) string {
	if len(s) <= 4 {
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}

func pick[T any](items []T) T {
	var b [1]byte
	rand.Read(b[:])
	return items[int(b[0])%len(items)]
}
