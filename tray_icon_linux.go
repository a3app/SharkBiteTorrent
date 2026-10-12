package main

import _ "embed"

// Linux tray icons (StatusNotifierItem) take PNG.
//
//go:embed build/appicon.png
var trayIcon []byte
