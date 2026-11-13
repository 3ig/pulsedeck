package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Start at login is a per-user LaunchAgent: no admin rights, and removable by
// deleting one file. It takes effect at the next login.
func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "app.pulsedeck.server.plist")
}

func autostartEnabled() (enabled, supported bool) {
	_, err := os.Stat(launchAgentPath())
	return err == nil, true
}

func setAutostart(on bool) error {
	path := launchAgentPath()
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>app.pulsedeck.server</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, exePath())
	return os.WriteFile(path, []byte(plist), 0o644)
}
