package main

import (
	"embed"
	"fmt"
	"os"
	"strings"

	_ "a.local/earlyinit" // must stay: see package doc
	"sharkbite/internal/winassoc"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Called by the installer/uninstaller; do the work and exit without a window.
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "--") {
		exe, _ := os.Executable()
		var err error
		switch os.Args[1] {
		case "--register-associations":
			err = winassoc.Register(exe)
		case "--unregister-associations":
			err = winassoc.Unregister(exe)
		default:
			err = fmt.Errorf("unknown option %s", os.Args[1])
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Shark BiteTorrent",
		Width:     1100,
		Height:    700,
		MinWidth:  720,
		MinHeight: 460,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 24, G: 26, B: 31, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		// A second launch (e.g. clicking a magnet link while running) hands
		// its arguments to this instance instead of starting another.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "sharkbitetorrent-3f6c2b1e",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
