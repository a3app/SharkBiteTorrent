// enginecheck downloads a torrent through the WARP tunnel for a while and
// prints progress. Optionally applies a speed limit and finally removes the
// torrent with its files, verifying they're gone.
//
//	go run ./cmd/enginecheck [-seconds 60] [-down KB/s] [-delete] <magnet or .torrent>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "a.local/earlyinit" // see package doc
	"sharkbite/internal/engine"
	"sharkbite/internal/tunnel"
	"sharkbite/internal/warp"
)

func main() {
	secs := flag.Int("seconds", 60, "how long to run")
	down := flag.Int64("down", 0, "download limit in KB/s (0 = unlimited)")
	del := flag.Bool("delete", false, "remove the torrent and its files at the end")
	recheck := flag.Bool("recheck", false, "verify existing data first, as the app does after a restart")
	dirFlag := flag.String("dir", "", "download folder (default: a temp folder)")
	state := flag.String("state", "", "folder for the progress database (default: in memory)")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	dir, _ := os.UserConfigDir()
	acc, err := warp.LoadOrRegister(filepath.Join(dir, "SharkBiteTorrent", "warp.json"))
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

	out := filepath.Join(os.TempDir(), "sharkbite-enginecheck")
	if *dirFlag != "" {
		out = *dirFlag
	}
	e, err := engine.New(t, engine.Options{DownLimit: *down * 1024, StateDir: *state})
	if err != nil {
		fmt.Println("engine:", err)
		os.Exit(1)
	}
	defer e.Close()
	hash, err := e.Add(engine.AddOptions{Source: flag.Arg(0), Dir: out, Recheck: *recheck})
	if err != nil {
		fmt.Println("add:", err)
		os.Exit(1)
	}
	fmt.Println("saving to", out)

	for i := 0; i < *secs/2; i++ {
		time.Sleep(2 * time.Second)
		for _, s := range e.List() {
			fmt.Printf("[%3ds] %-10s %5.1f%%  down %8.1f KB/s  peers %d seeds %d  %s\n",
				(i+1)*2, s.State, s.Progress*100, float64(s.DownRate)/1024, s.Peers, s.Seeds, s.Name)
		}
	}

	if os.Getenv("ENGINE_STATUS") != "" {
		e.WriteStatus(os.Stdout)
	}

	if *del {
		entries, _ := os.ReadDir(out)
		for _, en := range entries {
			fmt.Println("  before delete:", en.Name())
		}
		if err := e.Remove(hash, true); err != nil {
			fmt.Println("remove:", err)
			os.Exit(1)
		}
		entries, _ = os.ReadDir(out)
		for _, en := range entries {
			fmt.Println("  left behind:", en.Name())
		}
		fmt.Printf("after delete: %d item(s) left\n", len(entries))
	}
}
