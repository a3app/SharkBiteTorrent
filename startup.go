package main

import (
	"errors"
	"runtime"

	"sharkbite/internal/platform"
	"sharkbite/internal/systool"
)

// StartupStatus is the "Start when I sign in" option in Settings.
type StartupStatus struct {
	Enabled bool `json:"enabled"`
	// ViaWindowsSettings: the Microsoft Store version can't add itself to
	// startup; Windows lists it under Settings → Apps → Startup instead.
	ViaWindowsSettings bool `json:"viaWindowsSettings"`
}

func (a *App) GetStartup() StartupStatus {
	if storeBuild {
		return StartupStatus{ViaWindowsSettings: true}
	}
	return StartupStatus{Enabled: platform.StartupEnabled()}
}

func (a *App) SetStartup(on bool) error {
	if storeBuild {
		return errors.New("use Windows Settings → Apps → Startup")
	}
	return platform.SetStartup(a.exe, on)
}

// OpenStartupSettings opens Windows' Startup apps page (Store version).
func (a *App) OpenStartupSettings() error {
	if runtime.GOOS != "windows" {
		return errors.New("not available here")
	}
	return systool.Command("explorer", "ms-settings:startupapps").Start()
}
