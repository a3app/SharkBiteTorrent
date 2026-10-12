package main

// The systray library needs the main thread on macOS, which Wails v2 owns, so
// the tray icon is disabled there for now; closing the window just hides it
// and the Dock icon brings it back.

func (a *App) runTray() {}

func quitTray() {}
