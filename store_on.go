//go:build store

package main

// storeBuild is set for the Microsoft Store package (-tags store). The Store
// installs updates and registers magnet/.torrent from the package manifest,
// so the app's own updater and registry associations are turned off.
const storeBuild = true
