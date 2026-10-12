package tunnel

import (
	"bufio"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// ParseWGQuick parses a standard WireGuard .conf file (as exported by
// Mullvad, Proton, AirVPN, etc.). Only the first [Peer] is used, and
// AllowedIPs is ignored because all of this app's traffic goes through the
// tunnel anyway.
func ParseWGQuick(text string) (Config, error) {
	var cfg Config
	section := ""
	peers := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "peer" {
				peers++
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch {
		case section == "interface" && k == "privatekey":
			cfg.PrivateKey = v
		case section == "interface" && k == "address":
			cfg.Addresses = append(cfg.Addresses, splitList(v)...)
		case section == "interface" && k == "dns":
			for _, d := range splitList(v) {
				// wg-quick allows search domains in DNS; keep only IPs.
				if _, err := netip.ParseAddr(d); err == nil {
					cfg.DNS = append(cfg.DNS, d)
				}
			}
		case section == "interface" && k == "mtu":
			cfg.MTU, _ = strconv.Atoi(v)
		case section == "peer" && peers == 1 && k == "publickey":
			cfg.PeerPublicKey = v
		case section == "peer" && peers == 1 && k == "presharedkey":
			cfg.PresharedKey = v
		case section == "peer" && peers == 1 && k == "endpoint":
			cfg.Endpoint = v
		}
	}
	switch {
	case cfg.PrivateKey == "":
		return cfg, fmt.Errorf("config has no [Interface] PrivateKey")
	case len(cfg.Addresses) == 0:
		return cfg, fmt.Errorf("config has no [Interface] Address")
	case cfg.PeerPublicKey == "":
		return cfg, fmt.Errorf("config has no [Peer] PublicKey")
	case cfg.Endpoint == "":
		return cfg, fmt.Errorf("config has no [Peer] Endpoint")
	}
	if len(cfg.DNS) == 0 {
		cfg.DNS = []string{"1.1.1.1"}
	}
	for _, k := range []string{cfg.PrivateKey, cfg.PeerPublicKey} {
		if _, err := b64ToHex(k); err != nil {
			return cfg, fmt.Errorf("invalid key in config: %w", err)
		}
	}
	return cfg, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
