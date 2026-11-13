package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Start at login uses the desktop's standard autostart folder, so it works on
// GNOME, KDE, COSMIC and others without root.
func autostartFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "autostart", "pulsedeck.desktop")
}

func autostartEnabled() (enabled, supported bool) {
	path := autostartFile()
	if path == "" {
		return false, false
	}
	_, err := os.Stat(path)
	return err == nil, true
}

func setAutostart(on bool) error {
	path := autostartFile()
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Pulsedeck\nComment=System dashboard server\nExec=%q\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", exePath())
	return os.WriteFile(path, []byte(entry), 0o644)
}
