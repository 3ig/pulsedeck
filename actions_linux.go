package main

import (
	"fmt"
	"os"
)

// sessionEnv points media and volume tools at the user's desktop session when
// the server was started outside it (over SSH, from a service, at boot).
func sessionEnv() []string {
	var env []string
	dir := fmt.Sprintf("/run/user/%d", os.Getuid())
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		env = append(env, "XDG_RUNTIME_DIR="+dir)
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+dir+"/bus")
	}
	return env
}

func mediaCmd(key string) []string {
	switch key {
	case "playpause":
		return []string{"playerctl", "play-pause"}
	case "next":
		return []string{"playerctl", "next"}
	case "prev":
		return []string{"playerctl", "previous"}
	case "volup":
		return []string{"pactl", "set-sink-volume", "@DEFAULT_SINK@", "+5%"}
	case "voldown":
		return []string{"pactl", "set-sink-volume", "@DEFAULT_SINK@", "-5%"}
	case "mute":
		return []string{"pactl", "set-sink-mute", "@DEFAULT_SINK@", "toggle"}
	}
	return nil
}

func openCmd(target string) []string { return []string{"xdg-open", target} }

func lockCmd() []string { return []string{"loginctl", "lock-session"} }
