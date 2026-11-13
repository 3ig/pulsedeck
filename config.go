package main

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk server configuration. It is created with defaults on
// first run and is the only place where actions and plugins can be defined:
// clients can trigger an action by id but can never supply a command.
type Config struct {
	// ID identifies this installation. Two systems can share one address (a
	// dual-boot PC, a reused IP); clients tell them apart by this.
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Port       int      `json:"port"`
	IntervalMs int      `json:"interval_ms"`
	Token      string   `json:"token"`
	Actions    []Action `json:"actions"`
	Plugins    []Plugin `json:"plugins"`
}

// Plugin is an external command whose stdout (a number or a short text) is
// published as the sensor "plugin.<id>".
type Plugin struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Unit       string   `json:"unit"`
	Min        float64  `json:"min"`
	Max        float64  `json:"max"`
	Command    []string `json:"command"`
	IntervalMs int      `json:"interval_ms"`
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "pulsedeck", "config.json")
}

func newToken() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func defaultActions() []Action {
	return []Action{
		{ID: "prev", Label: "Previous", Icon: "prev", Type: "media", Arg: "prev"},
		{ID: "playpause", Label: "Play / Pause", Icon: "play", Type: "media", Arg: "playpause"},
		{ID: "next", Label: "Next", Icon: "next", Type: "media", Arg: "next"},
		{ID: "voldown", Label: "Volume −", Icon: "voldown", Type: "media", Arg: "voldown"},
		{ID: "volup", Label: "Volume +", Icon: "volup", Type: "media", Arg: "volup"},
		{ID: "mute", Label: "Mute", Icon: "mute", Type: "media", Arg: "mute"},
		{ID: "lock", Label: "Lock screen", Icon: "lock", Type: "lock"},
	}
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	case os.IsNotExist(err):
		cfg.Actions = defaultActions()
		cfg.Plugins = []Plugin{}
	default:
		return nil, err
	}

	changed := false
	if cfg.ID == "" {
		cfg.ID = newToken()
		changed = true
	}
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
		cfg.Name = strings.TrimSuffix(cfg.Name, ".local")
		if cfg.Name == "" {
			cfg.Name = "computer"
		}
		changed = true
	}
	if cfg.Port == 0 {
		cfg.Port = 7878
		changed = true
	}
	if cfg.IntervalMs < 200 {
		cfg.IntervalMs = 1000
		changed = true
	}
	if cfg.Token == "" {
		cfg.Token = newToken()
		changed = true
	}
	if changed {
		if err := saveConfig(path, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// The file holds the pairing token, so keep it private to the user.
	return os.WriteFile(path, data, 0o600)
}
