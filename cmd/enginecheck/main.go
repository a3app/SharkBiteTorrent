// enginecheck downloads a torrent through the WARP tunnel for a while and
// prints progress. Run: go run ./cmd/enginecheck <magnet or .torrent> [seconds]
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"torrent-client/internal/engine"
	"torrent-client/internal/tunnel"
	"torrent-client/internal/warp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: enginecheck <magnet or .torrent> [seconds]")
		os.Exit(2)
	}
	secs := 60
	if len(os.Args) > 2 {
		secs, _ = strconv.Atoi(os.Args[2])
	}

	dir, _ := os.UserConfigDir()
	acc, err := warp.LoadOrRegister(filepath.Join(dir, "torrent-client", "warp.json"))
	if err != nil {
		fmt.Println("register:", err)
		os.Exit(1)
	}
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
	tr, err := t.CheckTrace(ctx)
	cancel()
	if err != nil {
		fmt.Println("trace:", err)
		os.Exit(1)
	}
	fmt.Printf("tunnel up, exit IP %s warp=%s\n", tr.IP, tr.Warp)

	out := filepath.Join(os.TempDir(), "torrent-client-enginecheck")
	e, err := engine.New(t, out)
	if err != nil {
		fmt.Println("engine:", err)
		os.Exit(1)
	}
	defer e.Close()
	if _, err := e.Add(os.Args[1]); err != nil {
		fmt.Println("add:", err)
		os.Exit(1)
	}
	fmt.Println("saving to", out)

	for i := 0; i < secs/2; i++ {
		time.Sleep(2 * time.Second)
		for _, s := range e.List() {
			fmt.Printf("[%3ds] %-10s %5.1f%%  down %7.1f KB/s  up %6.1f KB/s  peers %d seeds %d  %s\n",
				(i+1)*2, s.State, s.Progress*100, float64(s.DownRate)/1024, float64(s.UpRate)/1024, s.Peers, s.Seeds, s.Name)
		}
	}
}
