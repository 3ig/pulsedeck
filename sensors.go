package main

import (
	"context"
	"math"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Sensor describes one published reading. Max == 0 means "no natural range";
// clients auto-scale those.
type Sensor struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Group string  `json:"group"`
	Unit  string  `json:"unit"`
	Kind  string  `json:"kind"` // value | text | list
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
}

type mount struct{ id, path string }

// Collector gathers readings. Cheap sources are sampled on every tick; slow
// ones (processes, temperatures, GPU, plugins) run on their own loops and are
// merged in from a cache so they can never stall the broadcast.
type Collector struct {
	sensors []Sensor
	mounts  []mount
	temps   map[string]string // sensor key -> id
	hasIO   bool

	prevT          time.Time
	prevRx, prevTx uint64
	prevRd, prevWr uint64

	mu    sync.Mutex
	slow  map[string]any
	procs map[int32]*process.Process
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if s == "" {
		return "root"
	}
	return s
}

func round(f float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(f*p) / p
}

func NewCollector(cfg *Config) *Collector {
	c := &Collector{slow: map[string]any{}, procs: map[int32]*process.Process{}, temps: map[string]string{}}
	add := func(id, label, group, unit, kind string, min, max float64) {
		c.sensors = append(c.sensors, Sensor{id, label, group, unit, kind, min, max})
	}

	add("cpu.total", "CPU", "CPU", "%", "value", 0, 100)
	if per, err := cpu.Percent(0, true); err == nil {
		for i := range per {
			add("cpu.core."+strconv.Itoa(i), "Core "+strconv.Itoa(i+1), "CPU", "%", "value", 0, 100)
		}
	}
	if _, err := load.Avg(); err == nil && runtime.GOOS != "windows" { // Windows has no load average
		add("cpu.load1", "Load (1 min)", "CPU", "", "value", 0, 0)
	}

	add("mem.used_pct", "Memory", "Memory", "%", "value", 0, 100)
	add("mem.used", "Memory used", "Memory", "B", "value", 0, 0)
	if vm, err := mem.VirtualMemory(); err == nil {
		c.sensors[len(c.sensors)-1].Max = float64(vm.Total)
	}
	add("mem.swap_pct", "Swap", "Memory", "%", "value", 0, 100)

	for _, m := range listMounts() {
		c.mounts = append(c.mounts, m)
		add("disk."+m.id+".used_pct", "Disk "+m.path, "Disk", "%", "value", 0, 100)
		add("disk."+m.id+".free", "Free on "+m.path, "Disk", "B", "value", 0, 0)
	}
	if rd, wr, ok := diskTotals(); ok {
		c.hasIO, c.prevRd, c.prevWr = true, rd, wr
		add("disk.read", "Disk read", "Disk", "B/s", "value", 0, 0)
		add("disk.write", "Disk write", "Disk", "B/s", "value", 0, 0)
	}

	c.prevRx, c.prevTx = netTotals()
	add("net.rx", "Download", "Network", "B/s", "value", 0, 0)
	add("net.tx", "Upload", "Network", "B/s", "value", 0, 0)

	temps := readTemps()
	kinds := map[string]bool{}
	for _, t := range temps {
		kinds[t.Kind] = true
	}
	for _, a := range tempAggregates {
		if kinds[a.kind] {
			add("temp."+a.kind, a.label, "Temperature", "°C", "value", 20, 110)
		}
	}
	if nCPU := countKind(temps, "cpu"); nCPU > 1 {
		add("temp.cpu_max", "CPU hottest core", "Temperature", "°C", "value", 20, 110)
	}
	if len(temps) > 0 {
		add("temp.max", "Hottest sensor", "Temperature", "°C", "value", 20, 110)
	}
	for _, t := range temps {
		id := "temp." + slug(t.Key)
		if _, dup := c.temps[t.Key]; dup || t.Hidden || len(c.temps) >= 64 {
			continue
		}
		c.temps[t.Key] = id
		add(id, t.Label, "Temperature sensors", "°C", "value", 20, 110)
	}
	for i, f := range readFans() {
		add("fan."+strconv.Itoa(i), f.Label, "Fans", "rpm", "value", 0, f.Max)
	}
	if _, ok := platformPower(); ok {
		add("sys.power", "System power", "Power", "W", "value", 0, 0)
	}
	platformCPUHW() // first call only primes the counters
	time.Sleep(250 * time.Millisecond)
	hw := platformCPUHW()
	if !math.IsNaN(hw.cpuW) {
		add("cpu.power", "CPU power", "Power", "W", "value", 0, 0)
	}
	if len(hw.freqs) > 0 {
		top := 0.0
		for _, f := range hw.freqs {
			top = math.Max(top, f.Max)
		}
		add("cpu.freq", "CPU clock (average)", "CPU clock", "MHz", "value", 0, top)
		for i, f := range hw.freqs {
			add("cpu.freq."+strconv.Itoa(i), f.Label+" clock", "CPU clock", "MHz", "value", 0, f.Max)
		}
	}

	for i, g := range readGPUs() {
		p := "gpu." + strconv.Itoa(i) + "."
		has := func(v float64) bool { return !math.IsNaN(v) }
		if i == 0 && !has(g.power) {
			g.power = hw.gpuW
		}
		if has(g.util) {
			add(p+"util", g.name+" usage", "GPU", "%", "value", 0, 100)
		}
		if has(g.memUsed) && has(g.memTotal) && g.memTotal > 0 {
			add(p+"mem_pct", g.name+" memory", "GPU", "%", "value", 0, 100)
			add(p+"mem", g.name+" memory used", "GPU", "B", "value", 0, g.memTotal)
		}
		if has(g.temp) {
			add(p+"temp", g.name+" temperature", "GPU", "°C", "value", 20, 110)
		}
		if has(g.power) {
			add(p+"power", g.name+" power", "GPU", "W", "value", 0, 0)
		}
		if has(g.fan) {
			add(p+"fan", g.name+" fan", "GPU", "%", "value", 0, 100)
		}
	}

	for _, e := range platformExtras() {
		add(e.ID, e.Label, e.Group, e.Unit, "value", e.Min, e.Max)
	}

	add("sys.uptime", "Uptime", "System", "s", "value", 0, 0)
	add("sys.procs", "Processes", "System", "", "value", 0, 0)
	add("proc.top", "Top processes", "System", "", "list", 0, 0)

	for _, p := range cfg.Plugins {
		if p.ID == "" || len(p.Command) == 0 {
			continue
		}
		label := p.Label
		if label == "" {
			label = p.ID
		}
		add("plugin."+slug(p.ID), label, "Plugins", p.Unit, "value", p.Min, p.Max)
		go c.pluginLoop(p)
	}

	c.prevT = time.Now()
	go c.slowLoop()
	return c
}

func (c *Collector) Sensors() []Sensor { return c.sensors }

// Collect samples the fast sources and merges the slow cache.
func (c *Collector) Collect() map[string]any {
	v := map[string]any{}
	now := time.Now()
	dt := now.Sub(c.prevT).Seconds()
	if dt <= 0 {
		dt = 1
	}
	c.prevT = now

	if per, err := cpu.Percent(0, true); err == nil && len(per) > 0 {
		sum := 0.0
		for i, p := range per {
			sum += p
			v["cpu.core."+strconv.Itoa(i)] = round(p, 1)
		}
		v["cpu.total"] = round(sum/float64(len(per)), 1)
	}
	if l, err := load.Avg(); err == nil {
		v["cpu.load1"] = round(l.Load1, 2)
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		used := vm.Used
		if u, ok := platformMemUsed(); ok {
			used = u
		}
		v["mem.used_pct"] = round(float64(used)/float64(vm.Total)*100, 1)
		v["mem.used"] = float64(used)
	}
	if sw, err := mem.SwapMemory(); err == nil {
		v["mem.swap_pct"] = round(sw.UsedPercent, 1)
	}
	for _, m := range c.mounts {
		if u, err := disk.Usage(m.path); err == nil && u.Total > 0 {
			// (total-free)/total rather than Used: on APFS "used" only counts
			// the volume itself while free space is shared by the container.
			v["disk."+m.id+".used_pct"] = round(float64(u.Total-u.Free)/float64(u.Total)*100, 1)
			v["disk."+m.id+".free"] = float64(u.Free)
		}
	}
	if c.hasIO {
		if rd, wr, ok := diskTotals(); ok {
			v["disk.read"] = rate(rd, c.prevRd, dt)
			v["disk.write"] = rate(wr, c.prevWr, dt)
			c.prevRd, c.prevWr = rd, wr
		}
	}
	rx, tx := netTotals()
	v["net.rx"] = rate(rx, c.prevRx, dt)
	v["net.tx"] = rate(tx, c.prevTx, dt)
	c.prevRx, c.prevTx = rx, tx

	if up, err := host.Uptime(); err == nil {
		v["sys.uptime"] = float64(up)
	}

	c.mu.Lock()
	for k, val := range c.slow {
		v[k] = val
	}
	c.mu.Unlock()
	return v
}

func rate(cur, prev uint64, dt float64) float64 {
	if cur < prev { // counter reset
		return 0
	}
	return math.Round(float64(cur-prev) / dt)
}

func (c *Collector) setSlow(vals map[string]any) {
	c.mu.Lock()
	for k, v := range vals {
		c.slow[k] = v
	}
	c.mu.Unlock()
}

func (c *Collector) slowLoop() {
	for {
		out := map[string]any{}
		c.collectProcs(out)
		c.collectHardware(out)
		c.setSlow(out)
		time.Sleep(2500 * time.Millisecond)
	}
}

type procRow struct {
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
}

func (c *Collector) collectProcs(out map[string]any) {
	pids, err := process.Pids()
	if err != nil {
		return
	}
	out["sys.procs"] = float64(len(pids))
	alive := make(map[int32]bool, len(pids))
	rows := make([]procRow, 0, len(pids))
	for _, pid := range pids {
		alive[pid] = true
		p := c.procs[pid]
		if p == nil {
			if p, err = process.NewProcess(pid); err != nil {
				continue
			}
			c.procs[pid] = p
			p.Percent(0) // prime the delta; reported from the next pass on
			continue
		}
		pct, err := p.Percent(0)
		if err != nil {
			continue
		}
		rows = append(rows, procRow{CPU: round(pct, 1)})
		rows[len(rows)-1].Name = strconv.Itoa(int(pid)) // placeholder, resolved for the top rows only
	}
	for pid := range c.procs {
		if !alive[pid] {
			delete(c.procs, pid)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CPU > rows[j].CPU })
	if len(rows) > 6 {
		rows = rows[:6]
	}
	for i := range rows {
		pid, _ := strconv.Atoi(rows[i].Name)
		if p := c.procs[int32(pid)]; p != nil {
			if name, err := p.Name(); err == nil && name != "" {
				rows[i].Name = name
			}
			if mi, err := p.MemoryInfo(); err == nil && mi != nil {
				rows[i].Mem = float64(mi.RSS)
			}
		}
	}
	out["proc.top"] = rows
}

func (c *Collector) pluginLoop(p Plugin) {
	every := time.Duration(p.IntervalMs) * time.Millisecond
	if every < time.Second {
		every = 5 * time.Second
	}
	id := "plugin." + slug(p.ID)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), every)
		out, err := quiet(exec.CommandContext(ctx, p.Command[0], p.Command[1:]...)).Output()
		cancel()
		if err == nil {
			s := strings.TrimSpace(string(out))
			if f, perr := strconv.ParseFloat(s, 64); perr == nil {
				c.setSlow(map[string]any{id: f})
			} else {
				if len(s) > 120 {
					s = s[:120]
				}
				c.setSlow(map[string]any{id: s})
			}
		}
		time.Sleep(every)
	}
}

func listMounts() []mount {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	skipFS := map[string]bool{"squashfs": true, "tmpfs": true, "devfs": true, "devtmpfs": true, "overlay": true, "autofs": true, "efivarfs": true, "vfat": true}
	skipPrefix := []string{"/System/", "/private/", "/dev", "/snap", "/boot", "/run", "/sys", "/proc", "/var/lib", "/Library/"}
	seen := map[string]bool{}
	var out []mount
next:
	for _, p := range parts {
		if skipFS[strings.ToLower(p.Fstype)] || seen[p.Device] {
			continue
		}
		for _, pre := range skipPrefix {
			if strings.HasPrefix(p.Mountpoint, pre) {
				continue next
			}
		}
		if u, err := disk.Usage(p.Mountpoint); err != nil || u.Total == 0 {
			continue
		}
		seen[p.Device] = true
		out = append(out, mount{slug(p.Mountpoint), p.Mountpoint})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func diskTotals() (rd, wr uint64, ok bool) {
	io, err := disk.IOCounters()
	if err != nil || len(io) == 0 {
		return 0, 0, false
	}
	for _, d := range io {
		rd += d.ReadBytes
		wr += d.WriteBytes
	}
	return rd, wr, true
}

var virtualNIC = []string{"lo", "utun", "awdl", "llw", "bridge", "docker", "veth", "br-", "gif", "stf", "anpi", "ap", "vmnet", "virbr", "tailscale", "isatap", "teredo"}

func netTotals() (rx, tx uint64) {
	nics, err := net.IOCounters(true)
	if err != nil {
		return
	}
next:
	for _, n := range nics {
		name := strings.ToLower(n.Name)
		for _, pre := range virtualNIC {
			if strings.HasPrefix(name, pre) {
				continue next
			}
		}
		rx += n.BytesRecv
		tx += n.BytesSent
	}
	return
}

var tempAggregates = []struct{ kind, label string }{
	{"cpu", "CPU temperature"},
	{"gpu", "GPU temperature"},
	{"ssd", "Storage temperature"},
	{"battery", "Battery temperature"},
}

func countKind(temps []tempReading, kind string) (n int) {
	for _, t := range temps {
		if t.Kind == kind {
			n++
		}
	}
	return
}

// collectHardware samples temperatures, fans, power and GPUs. Readings a
// device does not provide are simply left out of the frame.
func (c *Collector) collectHardware(out map[string]any) {
	put := func(id string, v float64, places int) {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out[id] = round(v, places)
		}
	}
	sum, cnt, hottest, cpuMax := map[string]float64{}, map[string]float64{}, 0.0, 0.0
	for _, t := range readTemps() {
		hottest = math.Max(hottest, t.C)
		if t.Kind == "cpu" {
			cpuMax = math.Max(cpuMax, t.C)
		}
		sum[t.Kind] += t.C
		cnt[t.Kind]++
		if id, ok := c.temps[t.Key]; ok {
			put(id, t.C, 1)
		}
	}
	for _, a := range tempAggregates {
		if cnt[a.kind] > 0 {
			put("temp."+a.kind, sum[a.kind]/cnt[a.kind], 1)
		}
	}
	if cnt["cpu"] > 1 {
		put("temp.cpu_max", cpuMax, 1)
	}
	if hottest > 0 {
		put("temp.max", hottest, 1)
	}
	for i, f := range readFans() {
		put("fan."+strconv.Itoa(i), f.RPM, 0)
	}
	if w, ok := platformPower(); ok {
		put("sys.power", w, 1)
	}
	for _, e := range platformExtras() {
		put(e.ID, e.Value, 3)
	}
	hw := platformCPUHW()
	put("cpu.power", hw.cpuW, 2)
	if len(hw.freqs) > 0 {
		sum := 0.0
		for i, f := range hw.freqs {
			sum += f.MHz
			put("cpu.freq."+strconv.Itoa(i), f.MHz, 0)
		}
		put("cpu.freq", sum/float64(len(hw.freqs)), 0)
	}
	for i, g := range readGPUs() {
		p := "gpu." + strconv.Itoa(i) + "."
		if i == 0 && math.IsNaN(g.power) {
			g.power = hw.gpuW
		}
		put(p+"util", g.util, 1)
		put(p+"temp", g.temp, 1)
		put(p+"power", g.power, 2)
		put(p+"fan", g.fan, 0)
		put(p+"mem", g.memUsed, 0)
		if g.memTotal > 0 {
			put(p+"mem_pct", g.memUsed/g.memTotal*100, 1)
		}
	}
}
