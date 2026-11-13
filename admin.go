package main

// The settings page: a small web UI served only to this computer, where the
// user pairs devices and edits what used to require hand-editing config.json.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"rsc.io/qr"
)

//go:embed admin
var adminFS embed.FS

// localOnly admits requests that really come from a browser on this machine:
// a loopback peer, a loopback Host header (so a hostile site cannot reach the
// page through DNS rebinding), and for anything that changes state a custom
// header that cross-site forms and scripts cannot send.
func localOnly(next http.HandlerFunc) http.HandlerFunc {
	loopbackHost := func(hostport string) bool {
		host := hostport
		if h, _, err := net.SplitHostPort(hostport); err == nil {
			host = h
		}
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		return ip != nil && ip.IsLoopback()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.RemoteAddr) || !loopbackHost(r.Host) {
			http.Error(w, "Settings can only be opened on the computer running Pulsedeck.", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
			if r.Header.Get("X-Pulsedeck-Admin") != "1" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

func (a *App) adminRoutes(mux *http.ServeMux) {
	static, _ := fs.Sub(adminFS, "admin")
	files := http.StripPrefix("/admin/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/admin/", localOnly(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/", http.StatusFound) })
	mux.HandleFunc("/api/admin/state", localOnly(a.adminState))
	mux.HandleFunc("/api/admin/qr.png", localOnly(a.adminQR))
	mux.HandleFunc("/api/admin/config", localOnly(a.adminConfig))
	mux.HandleFunc("/api/admin/token", localOnly(a.adminToken))
	mux.HandleFunc("/api/admin/autostart", localOnly(a.adminAutostart))
	mux.HandleFunc("/api/admin/test", localOnly(a.adminTest))
	mux.HandleFunc("/api/admin/quit", localOnly(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); a.quit() }()
	}))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (a *App) adminState(w http.ResponseWriter, r *http.Request) {
	type device struct {
		Addr  string `json:"addr"`
		Kind  string `json:"kind"`
		Since int64  `json:"since"`
	}
	h := a.hub
	h.mu.Lock()
	devices := make([]device, 0, len(h.subs))
	for c := range h.subs {
		host, _, _ := net.SplitHostPort(c.addr)
		devices = append(devices, device{host, deviceKind(c.agent), c.since.UnixMilli()})
	}
	cfg := *a.cfg
	h.mu.Unlock()
	sort.Slice(devices, func(i, j int) bool { return devices[i].Since < devices[j].Since })

	groups := map[string]int{}
	for _, s := range h.col.Sensors() {
		groups[s.Group]++
	}
	auto, autoOK := autostartEnabled()
	writeJSON(w, map[string]any{
		"version": version, "os": runtime.GOOS, "config_path": a.cfgPath,
		"name": cfg.Name, "port": cfg.Port, "interval_ms": cfg.IntervalMs, "token": cfg.Token,
		"pair_url": a.pairURL(), "addresses": lanIPs(),
		"devices": devices, "actions": cfg.Actions, "plugins": cfg.Plugins,
		"sensor_groups": groups, "sensor_count": len(h.col.Sensors()),
		"autostart": auto, "autostart_supported": autoOK,
	})
}

func deviceKind(agent string) string {
	switch {
	case strings.Contains(agent, "Android"):
		return "Android"
	case strings.Contains(agent, "iPhone"), strings.Contains(agent, "iPad"):
		return "iOS"
	case strings.Contains(agent, "Macintosh"):
		return "Mac browser"
	case strings.Contains(agent, "Windows"):
		return "Windows browser"
	case strings.Contains(agent, "Linux"):
		return "Linux browser"
	case agent == "":
		return "App"
	}
	return "Browser"
}

func (a *App) adminQR(w http.ResponseWriter, r *http.Request) {
	target := a.pairURL()
	if r.URL.Query().Has("demo") { // documentation screenshots: nothing real in the code
		target = "http://192.168.1.20:7878/#token=k7m2x9pq4hvd"
	}
	code, err := qr.Encode(target, qr.M)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	code.Scale = 8
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(code.PNG())
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// adminConfig saves edited settings. Buttons take effect immediately; the
// name, port, update rate and plugins need the server to restart, which it
// does by itself (the page waits and reconnects).
func (a *App) adminConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name       *string   `json:"name"`
		Port       *int      `json:"port"`
		IntervalMs *int      `json:"interval_ms"`
		Actions    *[]Action `json:"actions"`
		Plugins    *[]Plugin `json:"plugins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "That did not look like settings.", http.StatusBadRequest)
		return
	}
	fail := func(msg string) { http.Error(w, msg, http.StatusBadRequest) }

	a.hub.mu.Lock()
	next := *a.cfg
	a.hub.mu.Unlock()
	restart := false
	if in.Name != nil && strings.TrimSpace(*in.Name) != next.Name {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 48 {
			fail("The name must be between 1 and 48 characters.")
			return
		}
		next.Name, restart = name, true
	}
	if in.Port != nil && *in.Port != next.Port {
		if *in.Port < 1024 || *in.Port > 65535 {
			fail("The port must be between 1024 and 65535.")
			return
		}
		next.Port, restart = *in.Port, true
	}
	if in.IntervalMs != nil && *in.IntervalMs != next.IntervalMs {
		if *in.IntervalMs < 200 || *in.IntervalMs > 10000 {
			fail("The update interval must be between 0.2 and 10 seconds.")
			return
		}
		next.IntervalMs, restart = *in.IntervalMs, true
	}
	if in.Actions != nil {
		seen := map[string]bool{}
		for i, act := range *in.Actions {
			act.ID, act.Label = strings.TrimSpace(act.ID), strings.TrimSpace(act.Label)
			if act.ID == "" {
				act.ID = slug(act.Label)
			}
			switch {
			case !idRe.MatchString(act.ID) || seen[act.ID]:
				fail(fmt.Sprintf("Button %q needs a unique name.", act.Label))
				return
			case act.Label == "":
				fail("Every button needs a label.")
				return
			case act.Type == "media" && len(mediaCmd(act.Arg)) == 0:
				fail(fmt.Sprintf("Button %q: choose which media key to press.", act.Label))
				return
			case act.Type == "open" && strings.TrimSpace(act.Arg) == "":
				fail(fmt.Sprintf("Button %q: enter what to open.", act.Label))
				return
			case act.Type == "command" && len(act.Command) == 0:
				fail(fmt.Sprintf("Button %q: enter the command to run.", act.Label))
				return
			case act.Type != "media" && act.Type != "open" && act.Type != "lock" && act.Type != "command":
				fail(fmt.Sprintf("Button %q has an unknown type.", act.Label))
				return
			}
			seen[act.ID] = true
			(*in.Actions)[i] = act
		}
		next.Actions = *in.Actions
	}
	if in.Plugins != nil {
		seen := map[string]bool{}
		for i, p := range *in.Plugins {
			p.ID = slug(strings.TrimSpace(p.ID))
			if p.ID == "" || seen[p.ID] || len(p.Command) == 0 {
				fail("Every plugin needs a unique name and a command.")
				return
			}
			seen[p.ID] = true
			(*in.Plugins)[i] = p
		}
		before, _ := json.Marshal(next.Plugins)
		after, _ := json.Marshal(*in.Plugins)
		if string(before) != string(after) {
			next.Plugins, restart = *in.Plugins, true
		}
	}

	if err := saveConfig(a.cfgPath, &next); err != nil {
		http.Error(w, "Could not save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if restart {
		writeJSON(w, map[string]any{"ok": true, "restart": true, "port": next.Port})
		go func() { time.Sleep(300 * time.Millisecond); restartSelf() }()
		return
	}
	a.hub.mu.Lock()
	a.cfg.Actions = next.Actions
	a.hub.mu.Unlock()
	a.hub.rehello()
	writeJSON(w, map[string]any{"ok": true, "restart": false})
}

// adminToken issues a new pairing token; devices paired with the old one keep
// monitoring but their buttons stop working until they are paired again.
func (a *App) adminToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.hub.mu.Lock()
	a.cfg.Token = newToken()
	next := *a.cfg
	a.hub.mu.Unlock()
	if err := saveConfig(a.cfgPath, &next); err != nil {
		http.Error(w, "Could not save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) adminAutostart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := setAutostart(in.Enabled); err != nil {
		http.Error(w, "Could not change the login setting: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// adminTest runs a button exactly as a paired device would.
func (a *App) adminTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a.hub.mu.Lock()
	token := a.cfg.Token
	a.hub.mu.Unlock()
	if err := a.hub.trigger(in.ID, token); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return exe
}
