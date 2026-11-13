//go:build darwin && !cgo

package main

// Built without cgo (cross-compiled): the macOS menu-bar icon needs Cocoa, so
// this build runs as a plain background server.
func trayAvailable() bool { return false }
func runTray(a *App)      {}
