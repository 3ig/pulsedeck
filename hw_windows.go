package main

// Windows hardware readings.
//
// Built in (no admin, no driver): per-core clock speed, CPU package power and
// GPU usage / memory, all from performance counters.
//
// Temperatures, fan speeds, voltages and the like are not available from
// Windows itself without a kernel driver. When HWiNFO is running with "Shared
// Memory Support" enabled, every reading it shows is published too, grouped
// by HWiNFO's own sensor names, and its temperatures and fans feed the
// standard summary sensors (CPU temperature, Fan 1, ...).

import (
	"encoding/binary"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ctrCorePerf = `\Processor Information(*)\% Processor Performance`
	ctrCoreFreq = `\Processor Information(*)\Processor Frequency`
	ctrEnergy   = `\Energy Meter(*)\Power`
	ctrGPUUtil  = `\GPU Engine(*)\Utilization Percentage`
	ctrGPUMem   = `\GPU Adapter Memory(*)\Dedicated Usage`
)

var winPDH struct {
	once sync.Once
	q    *pdhQuery
}

func pdh() *pdhQuery {
	winPDH.once.Do(func() { winPDH.q = newPdhQuery(ctrCorePerf, ctrCoreFreq, ctrEnergy, ctrGPUUtil, ctrGPUMem) })
	return winPDH.q
}

var rePkgPower = regexp.MustCompile(`(?i)^rapl_package\d+_pkg$`)

// platformCPUHW is called once per slow tick and is the single place that
// advances the PDH query, so every reading in a tick comes from one sample.
func platformCPUHW() cpuHW {
	hw := cpuHW{cpuW: nan, gpuW: nan}
	q := pdh()
	if q == nil || !q.collect() {
		return hw
	}
	// "% Processor Performance" is relative to the nominal frequency and goes
	// above 100 when the core is boosting, which is exactly the clock speed.
	perf, base := q.values(ctrCorePerf), q.values(ctrCoreFreq)
	type core struct {
		group, index int
		mhz, nominal float64
	}
	var cores []core
	for name, pct := range perf {
		parts := strings.Split(name, ",")
		if len(parts) != 2 {
			continue
		}
		g, err1 := strconv.Atoi(parts[0])
		i, err2 := strconv.Atoi(parts[1])
		nominal := base[name]
		if err1 != nil || err2 != nil || nominal <= 0 { // "_Total" rows
			continue
		}
		cores = append(cores, core{g, i, pct / 100 * nominal, nominal})
	}
	sort.Slice(cores, func(a, b int) bool {
		if cores[a].group != cores[b].group {
			return cores[a].group < cores[b].group
		}
		return cores[a].index < cores[b].index
	})
	for n, c := range cores {
		// Windows does not report the boost ceiling; 1.4x nominal is a sensible
		// full-scale for the gauge on current desktop and laptop chips.
		hw.freqs = append(hw.freqs, coreFreq{Label: "CPU " + strconv.Itoa(n+1), MHz: c.mhz, Max: math.Round(c.nominal*1.4/100) * 100})
	}
	for name, mw := range q.values(ctrEnergy) {
		if rePkgPower.MatchString(name) {
			hw.cpuW = nanAdd(hw.cpuW, mw/1000)
		}
	}
	return hw
}

func nanAdd(a, b float64) float64 {
	if a != a {
		return b
	}
	return a + b
}

var reGPUEngine = regexp.MustCompile(`(?i)luid_(0x[0-9a-f]+_0x[0-9a-f]+)_phys_\d+_eng_\d+_engtype_(\w+)`)
var reGPUAdapter = regexp.MustCompile(`(?i)luid_(0x[0-9a-f]+_0x[0-9a-f]+)_phys_\d+`)

// platformGPUs covers every GPU vendor through the counters Task Manager
// uses. NVIDIA cards are left to nvidia-smi (hw.go), which also reports
// temperature, power and fan.
func platformGPUs() []gpuStat {
	if nvidiaErr == nil {
		return nil
	}
	q := pdh()
	if q == nil {
		return nil
	}
	// Usage per adapter and engine type, summed over processes; the busiest
	// engine type is the adapter's usage (as in Task Manager).
	engines := map[string]map[string]float64{}
	for name, v := range q.values(ctrGPUUtil) {
		m := reGPUEngine.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		luid := strings.ToLower(m[1])
		if engines[luid] == nil {
			engines[luid] = map[string]float64{}
		}
		engines[luid][strings.ToLower(m[2])] += v
	}
	mem := map[string]float64{}
	for name, v := range q.values(ctrGPUMem) {
		if m := reGPUAdapter.FindStringSubmatch(name); m != nil {
			mem[strings.ToLower(m[1])] = v
		}
	}
	// Software adapters show up too; keep the ones that hold dedicated memory.
	var luids []string
	for luid, used := range mem {
		if used > 0 {
			luids = append(luids, luid)
		}
	}
	sort.Slice(luids, func(a, b int) bool { return mem[luids[a]] > mem[luids[b]] })
	var gpus []gpuStat
	for i, luid := range luids {
		busiest := 0.0
		for _, v := range engines[luid] {
			busiest = math.Max(busiest, v)
		}
		name := "GPU"
		if len(luids) > 1 {
			name = "GPU " + strconv.Itoa(i+1)
		}
		gpus = append(gpus, gpuStat{name: name, util: math.Min(100, busiest), memUsed: mem[luid], memTotal: nan, temp: nan, power: nan, fan: nan})
	}
	return gpus
}

func platformPower() (float64, bool)  { return 0, false }
func platformMemUsed() (uint64, bool) { return 0, false }

// ---- HWiNFO shared memory ----

const (
	hwinfoMapName   = `Global\HWiNFO_SENS_SM2`
	hwinfoSignature = 0x53695748 // "HWiS": the table is live
	hwinfoTemp      = 1
	hwinfoFan       = 3
)

var procOpenFileMapping = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenFileMappingW")

type hwinfoReading struct {
	kind   uint32 // 1 temp, 2 voltage, 3 fan, 4 current, 5 power, 6 clock, 7 usage, 8 other
	id     string // stable across HWiNFO restarts: sensor id, instance, reading id
	sensor string
	label  string
	unit   string
	value  float64
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// readHWiNFO copies the sensor table out of HWiNFO's shared memory. It returns
// nil when HWiNFO is not running or its shared memory support is switched off
// (the free version switches it off again after 12 hours).
func readHWiNFO() []hwinfoReading {
	name, _ := syscall.UTF16PtrFromString(hwinfoMapName)
	h, _, _ := procOpenFileMapping.Call(windows.FILE_MAP_READ, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil
	}
	defer windows.CloseHandle(windows.Handle(h))
	addr, err := windows.MapViewOfFile(windows.Handle(h), windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil || addr == 0 {
		return nil
	}
	defer windows.UnmapViewOfFile(addr)

	var info windows.MemoryBasicInformation
	if windows.VirtualQuery(addr, &info, unsafe.Sizeof(info)) != nil || info.RegionSize < 44 {
		return nil
	}
	// addr is a mapped view, not Go memory; convert through a pointer-to-pointer
	// so vet's unsafe.Pointer check stays meaningful.
	mem := unsafe.Slice(*(**byte)(unsafe.Pointer(&addr)), info.RegionSize)
	u32 := func(off uint32) uint32 { return binary.LittleEndian.Uint32(mem[off:]) }
	if u32(0) != hwinfoSignature {
		return nil
	}
	sensorOff, sensorSize, sensorN := u32(20), u32(24), u32(28)
	readOff, readSize, readN := u32(32), u32(36), u32(40)
	if readSize < 316 || sensorSize < 264 || uint64(readOff)+uint64(readSize)*uint64(readN) > uint64(len(mem)) ||
		uint64(sensorOff)+uint64(sensorSize)*uint64(sensorN) > uint64(len(mem)) {
		return nil
	}
	type sensorInfo struct{ id, name string }
	sensors := make([]sensorInfo, sensorN)
	for i := uint32(0); i < sensorN; i++ {
		e := mem[sensorOff+i*sensorSize : sensorOff+(i+1)*sensorSize]
		name := cString(e[136:264])
		if sensorSize >= 392 { // newer layout carries UTF-8 names
			if s := cString(e[264:392]); s != "" {
				name = s
			}
		}
		sensors[i] = sensorInfo{strconv.FormatUint(uint64(binary.LittleEndian.Uint32(e[0:])), 16) + "_" + strconv.Itoa(int(binary.LittleEndian.Uint32(e[4:]))), name}
	}
	out := make([]hwinfoReading, 0, readN)
	for i := uint32(0); i < readN; i++ {
		e := mem[readOff+i*readSize : readOff+(i+1)*readSize]
		sensorIdx := binary.LittleEndian.Uint32(e[4:])
		if sensorIdx >= sensorN {
			continue
		}
		label, unit := cString(e[140:268]), cString(e[268:284])
		if readSize >= 460 {
			if s := cString(e[316:444]); s != "" {
				label = s
			}
			if s := cString(e[444:460]); s != "" {
				unit = s
			}
		}
		out = append(out, hwinfoReading{
			kind:   binary.LittleEndian.Uint32(e[0:]),
			id:     sensors[sensorIdx].id + "_" + strconv.FormatUint(uint64(binary.LittleEndian.Uint32(e[8:])), 16),
			sensor: sensors[sensorIdx].name,
			label:  label,
			unit:   unit,
			value:  math.Float64frombits(binary.LittleEndian.Uint64(e[284:])),
		})
	}
	return out
}

// hwinfoLabels makes reading labels unique: two drives both report "Drive
// Temperature", so repeats get their sensor's name appended.
func hwinfoLabels(rs []hwinfoReading) []string {
	count := map[string]int{}
	for _, r := range rs {
		count[r.label]++
	}
	labels := make([]string, len(rs))
	for i, r := range rs {
		labels[i] = r.label
		if count[r.label] > 1 && r.sensor != "" {
			labels[i] = r.label + " (" + r.sensor + ")"
		}
	}
	return labels
}

func hwinfoKind(r hwinfoReading) string {
	text := strings.ToLower(r.label + " " + r.sensor)
	switch {
	case strings.Contains(text, "gpu"):
		return "gpu"
	case strings.Contains(text, "s.m.a.r.t"), strings.Contains(text, "drive"), strings.Contains(text, "nvme"), strings.Contains(text, "ssd"):
		return "ssd"
	case strings.Contains(text, "cpu"), strings.Contains(text, "core"), strings.Contains(text, "tctl"), strings.Contains(text, "tdie"):
		// A motherboard header called "CPU" is still a CPU-area reading.
		return "cpu"
	}
	return ""
}

// hwinfoCache holds one copy of the table per slow tick: several collectors
// read from it in the same pass.
var hwinfoCache struct {
	mu   sync.Mutex
	at   time.Time
	rows []hwinfoReading
}

func hwinfo() []hwinfoReading {
	hwinfoCache.mu.Lock()
	defer hwinfoCache.mu.Unlock()
	if time.Since(hwinfoCache.at) > time.Second {
		hwinfoCache.rows, hwinfoCache.at = readHWiNFO(), time.Now()
	}
	return hwinfoCache.rows
}

// platformTemps feeds the summary sensors only; the individual readings are
// already published under their HWiNFO groups by platformExtras.
func platformTemps() []tempReading {
	var out []tempReading
	for _, r := range hwinfo() {
		if r.kind == hwinfoTemp && r.value > 5 && r.value < 130 {
			out = append(out, tempReading{Key: r.id, Label: r.label, Kind: hwinfoKind(r), C: r.value, Hidden: true})
		}
	}
	return out
}

// platformFans lists HWiNFO's fan tachometers (rpm), in HWiNFO's own order so
// "Fan 1" stays the same fan between runs.
func platformFans() []fanReading {
	var fans []hwinfoReading
	for _, r := range hwinfo() {
		if r.kind == hwinfoFan && r.unit != "%" {
			fans = append(fans, r)
		}
	}
	labels := hwinfoLabels(fans)
	out := make([]fanReading, 0, len(fans))
	for i, r := range fans {
		label := labels[i]
		if !strings.Contains(strings.ToLower(label), "fan") {
			label += " fan"
		}
		out = append(out, fanReading{Label: label, RPM: r.value})
	}
	return out
}

// platformExtras publishes every HWiNFO reading as its own sensor.
func platformExtras() []extraReading {
	rows := hwinfo()
	labels := make([]string, len(rows))
	seen := map[string]int{}
	for i, r := range rows {
		seen[r.sensor+"\x00"+r.label]++
		labels[i] = r.label
		if n := seen[r.sensor+"\x00"+r.label]; n > 1 {
			labels[i] += " #" + strconv.Itoa(n)
		}
	}
	out := make([]extraReading, 0, len(rows))
	for i, r := range rows {
		e := extraReading{ID: "hwinfo." + r.id, Label: labels[i], Group: "HWiNFO · " + r.sensor, Unit: r.unit, Value: r.value}
		switch {
		case r.kind == hwinfoTemp:
			e.Min, e.Max = 20, 110
		case r.unit == "%":
			e.Max = 100
		}
		out = append(out, e)
	}
	return out
}
