package main

// The systray library needs the main thread on macOS, which Wails v2 owns, so
// the tray icon is disabled there for now. Instead the app behaves like other
// Mac apps that keep running: the close button hides the app (Wails'
// HideWindowOnClose, i.e. [NSApp hide:]), clicking the Dock icon brings it
// back, and Cmd+Q / Dock → Quit quit. Wails v2 sends quit through
// OnBeforeClose too, then stops the run loop from a goroutine without
// posting an event, so the process would keep running. beforeClose must
// not cancel a real quit, and on macOS it stops the run loop itself.
const hideOnClose = true

func (a *App) runTray() {}

func quitTray() {}
