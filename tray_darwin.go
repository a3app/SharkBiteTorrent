package main

// The systray library needs the main thread on macOS, which Wails v2 owns, so
// the tray icon is disabled there for now. Instead the app behaves like other
// Mac apps that keep running: the close button hides the app (Wails'
// HideWindowOnClose, i.e. [NSApp hide:]), clicking the Dock icon brings it
// back, and Cmd+Q / Dock → Quit quit. Wails v2 sends quit through
// OnBeforeClose too, so beforeClose must not cancel it here.
const hideOnClose = true

func (a *App) runTray() {}

func quitTray() {}
