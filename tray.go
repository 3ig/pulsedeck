//go:build (darwin && cgo) || linux || windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"fyne.io/systray"
)

// trayAvailable reports whether there is a desktop to put an icon on. A Linux
// server started over SSH or from a console has none.
func trayAvailable() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
}

// runTray shows the tray / menu-bar icon and blocks until the user quits.
func runTray(a *App) {
	systray.Run(func() {
		if runtime.GOOS == "darwin" {
			systray.SetTemplateIcon(trayIcon(true), trayIcon(true))
		} else {
			systray.SetIcon(trayIcon(false))
		}
		systray.SetTooltip("Pulsedeck — " + a.cfg.Name)

		title := systray.AddMenuItem("Pulsedeck — "+a.cfg.Name, "")
		title.Disable()
		status := systray.AddMenuItem("No devices connected", "")
		status.Disable()
		systray.AddSeparator()
		pair := systray.AddMenuItem("Pair a device…", "Show the QR code and pairing token")
		dash := systray.AddMenuItem("Open dashboard", "Open this computer's dashboard in the browser")
		settings := systray.AddMenuItem("Settings…", "Buttons, plugins, name and port")
		systray.AddSeparator()
		auto, autoOK := autostartEnabled()
		login := systray.AddMenuItemCheckbox("Start at login", "Launch Pulsedeck when you log in", auto)
		if !autoOK {
			login.Hide()
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit Pulsedeck", "")

		go func() {
			last := -1
			for range time.Tick(2 * time.Second) {
				if n := a.hub.clientCount(); n != last {
					last = n
					switch n {
					case 0:
						status.SetTitle("No devices connected")
					case 1:
						status.SetTitle("1 device connected")
					default:
						status.SetTitle(fmt.Sprintf("%d devices connected", n))
					}
				}
			}
		}()
		go func() {
			for {
				select {
				case <-pair.ClickedCh:
					openBrowser(a.localURL("/admin/#pair"))
				case <-dash.ClickedCh:
					openBrowser(a.localURL("/"))
				case <-settings.ClickedCh:
					openBrowser(a.localURL("/admin/"))
				case <-login.ClickedCh:
					want := !login.Checked()
					if err := setAutostart(want); err == nil {
						if want {
							login.Check()
						} else {
							login.Uncheck()
						}
					}
				case <-quit.ClickedCh:
					systray.Quit()
				}
			}
		}()
	}, func() { a.quit() })
}
