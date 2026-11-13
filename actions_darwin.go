package main

func osa(lines ...string) []string {
	argv := []string{"osascript"}
	for _, l := range lines {
		argv = append(argv, "-e", l)
	}
	return argv
}

// macOS has no public API for the global media keys, so transport controls
// talk to whichever of Spotify / Music is running.
func playerScript(verb string) []string {
	return osa(
		`if application "Spotify" is running then`,
		`tell application "Spotify" to `+verb,
		`else if application "Music" is running then`,
		`tell application "Music" to `+verb,
		`end if`,
	)
}

func mediaCmd(key string) []string {
	switch key {
	case "playpause":
		return playerScript("playpause")
	case "next":
		return playerScript("next track")
	case "prev":
		return playerScript("previous track")
	case "volup":
		return osa(`set volume output volume ((output volume of (get volume settings)) + 6)`)
	case "voldown":
		return osa(`set volume output volume ((output volume of (get volume settings)) - 6)`)
	case "mute":
		return osa(`set volume output muted not (output muted of (get volume settings))`)
	}
	return nil
}

func openCmd(target string) []string { return []string{"open", target} }

func lockCmd() []string { return []string{"pmset", "displaysleepnow"} }

func sessionEnv() []string { return nil }
