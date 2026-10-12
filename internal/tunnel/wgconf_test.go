package tunnel

import "testing"

func TestParseWGQuick(t *testing.T) {
	cfg, err := ParseWGQuick(`
[Interface]
# Device: Example
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 10.64.1.2/32, fc00:bbbb:bbbb:bb01::1:2/128
DNS = 10.64.0.1, example.internal
MTU = 1320

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = /UwcSPg38hW/D9Y3tcS1FOV0K1wuURMbS0sesJEP5ak=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = vpn.example.com:51820

[Peer]
PublicKey = TrMvSoP4jYQlY6RIzBgbssQqY3vxI2Pi+y71lOWWXX0=
Endpoint = 192.0.2.1:51820
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrivateKey != "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=" ||
		cfg.PeerPublicKey != "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=" ||
		cfg.PresharedKey != "/UwcSPg38hW/D9Y3tcS1FOV0K1wuURMbS0sesJEP5ak=" ||
		cfg.Endpoint != "vpn.example.com:51820" || cfg.MTU != 1320 {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(cfg.Addresses) != 2 || cfg.Addresses[1] != "fc00:bbbb:bbbb:bb01::1:2/128" {
		t.Errorf("addresses: %v", cfg.Addresses)
	}
	if len(cfg.DNS) != 1 || cfg.DNS[0] != "10.64.0.1" {
		t.Errorf("dns: %v", cfg.DNS)
	}
}

func TestParseWGQuickMissingFields(t *testing.T) {
	if _, err := ParseWGQuick("[Interface]\nAddress = 10.0.0.2/32\n"); err == nil {
		t.Error("expected error for config without keys")
	}
}
