// Pulsedeck server: samples system readings and streams them to dashboard
// clients over a WebSocket. See PROTOCOL.md for the wire format.
package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/zeroconf/v2"
	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"
)

// version is stamped by release builds (-ldflags "-X main.version=...").
var version = "dev"

const (
	protoVer    = 1
	historyLen  = 120
	mdnsService = "_pulsedeck._tcp"
	// Origin of the web UI when it runs inside the native mobile shells.
	appOrigin = "pulsedeck.app"
)

//go:embed client
var clientFS embed.FS

type client struct {
	conn  *websocket.Conn
	send  chan []byte
	addr  string
	agent string
	since time.Time
}

type Hub struct {
	cfg  *Config
	col  *Collector
	mu   sync.Mutex
	subs map[*client]struct{}
	hist []sample
}

type sample struct {
	t int64
	v map[string]any
}

func (h *Hub) run() {
	tick := time.NewTicker(time.Duration(h.cfg.IntervalMs) * time.Millisecond)
	for range tick.C {
		s := sample{time.Now().UnixMilli(), h.col.Collect()}
		msg, _ := json.Marshal(map[string]any{"type": "data", "t": s.t, "v": s.v})
		h.mu.Lock()
		h.hist = append(h.hist, s)
		if len(h.hist) > historyLen {
			h.hist = h.hist[1:]
		}
		for c := range h.subs {
			select {
			case c.send <- msg:
			default: // too slow to keep up: drop it, the client reconnects
				delete(h.subs, c)
				close(c.send)
			}
		}
		h.mu.Unlock()
	}
}

// hello builds the handshake: what this server offers plus recent history so
// graphs are populated immediately. Caller holds h.mu.
func (h *Hub) hello() []byte {
	type pubAction struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Icon  string `json:"icon,omitempty"`
	}
	actions := make([]pubAction, 0, len(h.cfg.Actions))
	for _, a := range h.cfg.Actions {
		actions = append(actions, pubAction{a.ID, a.Label, a.Icon})
	}
	times := make([]int64, len(h.hist))
	series := map[string][]any{}
	for i, s := range h.hist {
		times[i] = s.t
		for k, v := range s.v {
			f, ok := v.(float64)
			if !ok {
				continue
			}
			if series[k] == nil {
				series[k] = make([]any, len(h.hist))
			}
			series[k][i] = f
		}
	}
	msg, _ := json.Marshal(map[string]any{
		"type":        "hello",
		"proto":       protoVer,
		"server":      map[string]any{"id": h.cfg.ID, "name": h.cfg.Name, "os": runtime.GOOS, "version": version},
		"interval_ms": h.cfg.IntervalMs,
		"sensors":     h.col.Sensors(),
		"actions":     actions,
		"history":     map[string]any{"t": times, "v": series},
	})
	return msg
}

var upgrader = websocket.Upgrader{CheckOrigin: originAllowed}

// originAllowed keeps arbitrary internet pages from reading this machine's
// stats through a visitor's browser. Native clients (no Origin), our own
// pages, the mobile shell and pages served from the local network may connect.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	hn := strings.ToLower(u.Hostname())
	if hn == appOrigin || hn == "localhost" || strings.HasSuffix(hn, ".local") {
		return true
	}
	ip := net.ParseIP(hn)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 8), addr: r.RemoteAddr, agent: r.UserAgent(), since: time.Now()}

	h.mu.Lock()
	c.send <- h.hello()
	h.subs[c] = struct{}{}
	h.mu.Unlock()

	go func() { // writer
		defer conn.Close()
		for msg := range c.send {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteMessage(websocket.TextMessage, msg) != nil {
				return
			}
		}
	}()

	defer func() {
		h.mu.Lock()
		if _, ok := h.subs[c]; ok {
			delete(h.subs, c)
			close(c.send)
		}
		h.mu.Unlock()
		conn.Close()
	}()
	conn.SetReadLimit(4096)
	for {
		var in struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Token string `json:"token"`
			N     int    `json:"n"`
		}
		if err := conn.ReadJSON(&in); err != nil {
			return
		}
		if in.Type != "action" {
			continue
		}
		res := map[string]any{"type": "action_result", "id": in.ID, "n": in.N, "ok": true}
		if err := h.trigger(in.ID, in.Token); err != nil {
			res["ok"], res["error"] = false, err.Error()
			log.Printf("action %q from %s failed: %v", in.ID, r.RemoteAddr, err)
		}
		msg, _ := json.Marshal(res)
		h.mu.Lock()
		if _, ok := h.subs[c]; ok {
			select {
			case c.send <- msg:
			default:
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) trigger(id, token string) error {
	h.mu.Lock()
	want, actions := h.cfg.Token, h.cfg.Actions
	h.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return fmt.Errorf("not paired: open the pairing link or enter the server token")
	}
	for _, a := range actions {
		if a.ID == id {
			return runAction(a)
		}
	}
	return fmt.Errorf("unknown action")
}

// lanIPs lists this machine's private IPv4 addresses, the one used for the
// default route first (machines with VMs or containers have many).
func lanIPs() []string {
	var out []string
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil { // no packet is sent
		out = append(out, c.LocalAddr().(*net.UDPAddr).IP.String())
		c.Close()
	}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() && (len(out) == 0 || out[0] != ipn.IP.String()) {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

// announce registers the DNS-SD service. Machines running containers or VMs
// have many private addresses; only the one on the default route is published
// (on its own interface), otherwise clients may be handed an address they
// cannot reach.
func announce(cfg *Config) (*zeroconf.Server, error) {
	txt := []string{"proto=" + strconv.Itoa(protoVer), "v=" + version}
	if ips := lanIPs(); len(ips) > 0 {
		ifaces, _ := net.Interfaces()
		for _, ifc := range ifaces {
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.String() == ips[0] {
					return zeroconf.RegisterProxy(cfg.Name, mdnsService, "local.", cfg.Port,
						slug(cfg.Name)+"-pulsedeck", []string{ips[0]}, txt, []net.Interface{ifc})
				}
			}
		}
	}
	return zeroconf.Register(cfg.Name, mdnsService, "local.", cfg.Port, txt, nil)
}

// rehello re-sends the handshake to every client, after the list of actions
// changed. Clients treat a new hello as a fresh start.
func (h *Hub) rehello() {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.hello()
	for c := range h.subs {
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (h *Hub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// App ties the pieces together for the settings page and the tray icon.
type App struct {
	cfg     *Config
	cfgPath string
	hub     *Hub
	quit    func()
}

func (a *App) localURL(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", a.cfg.Port, path)
}

// pairURL is what a phone or tablet should open: the dashboard on this
// machine's LAN address, carrying the pairing token in the fragment.
func (a *App) pairURL() string {
	host := "localhost"
	if ips := lanIPs(); len(ips) > 0 {
		host = ips[0]
	}
	a.hub.mu.Lock()
	defer a.hub.mu.Unlock()
	return fmt.Sprintf("http://%s:%d/#token=%s", host, a.cfg.Port, a.cfg.Token)
}

func openBrowser(target string) {
	if argv := openCmd(target); len(argv) > 0 {
		go runWait(argv)
	}
}

// restartSelf relaunches the server so settings that cannot change while
// running (port, name, update rate, plugins) take effect.
func restartSelf() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("restart: %v", err)
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "PULSEDECK_RESTART=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	detach(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("restart: %v", err)
		return
	}
	os.Exit(0)
}

// listen binds the port. If another Pulsedeck already owns it, that instance's
// settings page is opened instead of starting a second copy; after a restart
// the old process may still be letting go of the port, so wait briefly.
func listen(a *App) net.Listener {
	addr := ":" + strconv.Itoa(a.cfg.Port)
	deadline := time.Now().Add(6 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln
		}
		restarting := os.Getenv("PULSEDECK_RESTART") != ""
		if !restarting {
			c := http.Client{Timeout: time.Second}
			if resp, perr := c.Get(a.localURL("/api/ping")); perr == nil {
				body := make([]byte, 16)
				n, _ := resp.Body.Read(body)
				resp.Body.Close()
				if strings.HasPrefix(string(body[:n]), "pulsedeck") {
					fmt.Println("Pulsedeck is already running; opening its settings.")
					if argv := openCmd(a.localURL("/admin/")); len(argv) > 0 {
						runWait(argv)
					}
					os.Exit(0)
				}
			}
		}
		if !restarting || time.Now().After(deadline) {
			log.Fatalf("cannot listen on port %d: %v (another program is using it; change \"port\" in %s)", a.cfg.Port, err, a.cfgPath)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "path to config file")
	port := flag.Int("port", 0, "listen port (overrides config)")
	noQR := flag.Bool("no-qr", false, "do not print the pairing QR code")
	noTray := flag.Bool("no-tray", false, "run without the tray / menu-bar icon")
	showVer := flag.Bool("version", false, "print version and exit")
	listSensors := flag.Bool("sensors", false, "print every sensor this machine provides, with current values, and exit")
	flag.Parse()
	attachConsole()
	if *showVer {
		fmt.Println("pulsedeck", version)
		return
	}

	_, statErr := os.Stat(*cfgPath)
	firstRun := os.IsNotExist(statErr)
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config %s: %v", *cfgPath, err)
	}
	if *port != 0 {
		cfg.Port = *port
	}

	if *listSensors {
		col := NewCollector(cfg)
		time.Sleep(3500 * time.Millisecond) // let the slow sources report once
		col.Collect()
		time.Sleep(time.Second)
		vals := col.Collect()
		for _, s := range col.Sensors() {
			if s.Kind == "list" {
				continue
			}
			fmt.Printf("%-28s %-34s %v %s\n", s.ID, s.Label, vals[s.ID], s.Unit)
		}
		return
	}

	// Started by double-click (no terminal): keep a log file instead.
	interactive := term.IsTerminal(int(os.Stdout.Fd()))
	if !interactive && os.Getenv("PULSEDECK_RESTART") == "" {
		if f, err := os.OpenFile(filepath.Join(filepath.Dir(*cfgPath), "pulsedeck.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
			os.Stdout, os.Stderr = f, f
			log.SetOutput(f)
		}
	}

	app := &App{cfg: cfg, cfgPath: *cfgPath}
	ln := listen(app)
	app.hub = &Hub{cfg: cfg, col: NewCollector(cfg), subs: map[*client]struct{}{}}
	go app.hub.run()

	static, _ := fs.Sub(clientFS, "client")
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", app.hub.serveWS)
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "pulsedeck ", version) })
	app.adminRoutes(mux)
	mux.Handle("/", http.FileServer(http.FS(static)))

	// Announce on the local network so clients find us without typing an IP.
	// The announcement carries this machine's address, which changes when it
	// moves between networks or gets a new lease, so it is renewed whenever
	// the address does.
	var (
		mdnsMu sync.Mutex
		mdns   *zeroconf.Server
		mdnsIP string
	)
	primaryIP := func() string {
		if ips := lanIPs(); len(ips) > 0 {
			return ips[0]
		}
		return ""
	}
	reannounce := func() {
		ip := primaryIP()
		mdnsMu.Lock()
		defer mdnsMu.Unlock()
		if mdns != nil && ip == mdnsIP {
			return
		}
		if mdns != nil {
			mdns.Shutdown()
			mdns = nil
		}
		srv, err := announce(cfg)
		if err != nil {
			log.Printf("mDNS announce failed (clients must add this server by address): %v", err)
			return
		}
		mdns, mdnsIP = srv, ip
	}
	reannounce()
	go func() {
		for range time.Tick(15 * time.Second) {
			reannounce()
		}
	}()
	app.quit = func() {
		mdnsMu.Lock()
		if mdns != nil {
			mdns.Shutdown()
		}
		os.Exit(0)
	}

	// Say goodbye on the network when stopped, so devices do not keep listing
	// a computer that is gone until its announcement expires.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() { <-stop; app.quit() }()

	fmt.Printf("\n  Pulsedeck %s — %q (%s)\n\n", version, cfg.Name, runtime.GOOS)
	fmt.Printf("  Settings and pairing:  %s\n", app.localURL("/admin/"))
	fmt.Printf("  Dashboard on a device: %s\n", app.pairURL())
	fmt.Printf("  Pairing token:         %s\n\n", cfg.Token)
	if ips := lanIPs(); len(ips) > 0 && !*noQR && interactive {
		fmt.Println("  Scan to open the dashboard on a phone or tablet:")
		qrterminal.GenerateHalfBlock(app.pairURL(), qrterminal.L, os.Stdout)
	}

	go func() { log.Fatal(http.Serve(ln, mux)) }()

	tray := !*noTray && trayAvailable()
	// A first launch without a terminal has nothing else to show the user.
	if firstRun && (tray || !interactive) {
		openBrowser(app.localURL("/admin/"))
	}
	if tray {
		runTray(app) // blocks; owns the main thread, as macOS requires
		return
	}
	select {}
}
