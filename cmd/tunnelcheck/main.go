// tunnelcheck registers (or reuses) a WARP account, brings up the in-process
// tunnel and prints the exit IP. Run: go run ./cmd/tunnelcheck
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sharkbite/internal/tunnel"
	"sharkbite/internal/warp"
)

func main() {
	dir, _ := os.UserConfigDir()
	acc, err := warp.LoadOrRegister(filepath.Join(dir, "SharkBiteTorrent", "warp.json"))
	if err != nil {
		fmt.Println("register:", err)
		os.Exit(1)
	}
	fmt.Println("WARP account:", acc.ID, acc.AddressV4)

	t, err := tunnel.Start(tunnel.Config{
		PrivateKey:    acc.PrivateKey,
		PeerPublicKey: acc.PeerPublicKey,
		Endpoint:      acc.Endpoint,
		Addresses:     []string{acc.AddressV4, acc.AddressV6},
		DNS:           []string{"1.1.1.1", "1.0.0.1"},
	})
	if err != nil {
		fmt.Println("tunnel:", err)
		os.Exit(1)
	}
	defer t.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr, err := t.CheckTrace(ctx)
	if err != nil {
		fmt.Println("trace:", err)
		os.Exit(1)
	}
	fmt.Printf("Tunnel exit IP: %s (%s, colo %s, warp=%s)\n", tr.IP, tr.Location, tr.Colo, tr.Warp)
}
