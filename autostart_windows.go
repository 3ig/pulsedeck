package main

import (
	"golang.org/x/sys/windows/registry"
)

// Start at login is the per-user Run key: no admin rights needed.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() (enabled, supported bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, true
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Pulsedeck")
	return err == nil, true
}

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("Pulsedeck"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return k.SetStringValue("Pulsedeck", `"`+exePath()+`"`)
}
