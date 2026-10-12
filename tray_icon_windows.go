package main

import _ "embed"

// Windows tray icons must be .ico.
//
//go:embed build/windows/icon.ico
var trayIcon []byte
