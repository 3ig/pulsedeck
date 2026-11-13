package main

// CPU / GPU package power and per-core clock speed on Apple silicon.
//
// macOS only hands these out through the IOReport library (the same source
// `powermetrics` uses, but readable without root): energy counters in the
// "Energy Model" group and, for each core, how long it spent in each
// performance state. Clock speed is the residency-weighted mean of the
// frequencies of those states, which come from the power manager's tables in
// the I/O Registry.

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

var ior struct {
	once sync.Once
	ok   bool

	cfRelease        func(uintptr)
	cfStringCreate   func(alloc uintptr, s string, encoding uint32) uintptr
	cfStringGetCStr  func(str uintptr, buf *byte, size int, encoding uint32) bool
	cfDictGetValue   func(dict, key uintptr) uintptr
	cfArrayGetCount  func(arr uintptr) int
	cfArrayGetValue  func(arr uintptr, i int) uintptr
	cfDictCreateCopy func(alloc uintptr, capacity int, dict uintptr) uintptr

	copyChannels  func(group, subgroup uintptr, a, b, c uint64) uintptr
	mergeChannels func(a, b, nilArg uintptr)
	subscribe     func(alloc, channels uintptr, subscribed *uintptr, id uint64, nilArg uintptr) uintptr
	createSamples func(sub, channels, nilArg uintptr) uintptr
	samplesDelta  func(prev, cur, nilArg uintptr) uintptr
	chGroup       func(item uintptr) uintptr
	chSubGroup    func(item uintptr) uintptr
	chName        func(item uintptr) uintptr
	chUnit        func(item uintptr) uintptr
	simpleInt     func(item uintptr, idx int32) int64
	stateCount    func(item uintptr) int32
	stateName     func(item uintptr, idx int32) uintptr
	stateRes      func(item uintptr, idx int32) int64

	sub, channels uintptr
	keyChannels   uintptr
	prev          uintptr
	prevT         time.Time

	ecpuMHz, pcpuMHz []float64
}

const cfUTF8 = 0x08000100

func cfString(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	buf := make([]byte, 128)
	if !ior.cfStringGetCStr(ref, &buf[0], len(buf), cfUTF8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return ""
}

func iorOpen() bool {
	ior.once.Do(func() {
		defer func() { recover() }() // private library: any missing symbol just disables this source
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		lib, err := purego.Dlopen("/usr/lib/libIOReport.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&ior.cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&ior.cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&ior.cfStringGetCStr, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&ior.cfDictGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&ior.cfArrayGetCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&ior.cfArrayGetValue, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&ior.cfDictCreateCopy, cf, "CFDictionaryCreateMutableCopy")
		purego.RegisterLibFunc(&ior.copyChannels, lib, "IOReportCopyChannelsInGroup")
		purego.RegisterLibFunc(&ior.mergeChannels, lib, "IOReportMergeChannels")
		purego.RegisterLibFunc(&ior.subscribe, lib, "IOReportCreateSubscription")
		purego.RegisterLibFunc(&ior.createSamples, lib, "IOReportCreateSamples")
		purego.RegisterLibFunc(&ior.samplesDelta, lib, "IOReportCreateSamplesDelta")
		purego.RegisterLibFunc(&ior.chGroup, lib, "IOReportChannelGetGroup")
		purego.RegisterLibFunc(&ior.chSubGroup, lib, "IOReportChannelGetSubGroup")
		purego.RegisterLibFunc(&ior.chName, lib, "IOReportChannelGetChannelName")
		purego.RegisterLibFunc(&ior.chUnit, lib, "IOReportChannelGetUnitLabel")
		purego.RegisterLibFunc(&ior.simpleInt, lib, "IOReportSimpleGetIntegerValue")
		purego.RegisterLibFunc(&ior.stateCount, lib, "IOReportStateGetCount")
		purego.RegisterLibFunc(&ior.stateName, lib, "IOReportStateGetNameForIndex")
		purego.RegisterLibFunc(&ior.stateRes, lib, "IOReportStateGetResidency")

		energy := ior.copyChannels(ior.cfStringCreate(0, "Energy Model", cfUTF8), 0, 0, 0, 0)
		if energy == 0 {
			return
		}
		all := ior.cfDictCreateCopy(0, 0, energy)
		ior.cfRelease(energy)
		if cores := ior.copyChannels(ior.cfStringCreate(0, "CPU Stats", cfUTF8), ior.cfStringCreate(0, "CPU Core Performance States", cfUTF8), 0, 0, 0); cores != 0 {
			ior.mergeChannels(all, cores, 0)
			ior.cfRelease(cores)
		}
		ior.sub = ior.subscribe(0, all, &ior.channels, 0, 0)
		if ior.sub == 0 || ior.channels == 0 {
			return
		}
		ior.keyChannels = ior.cfStringCreate(0, "IOReportChannels", cfUTF8)
		ior.ecpuMHz, ior.pcpuMHz = pmgrFrequencies()
		ior.prev, ior.prevT = ior.createSamples(ior.sub, ior.channels, 0), time.Now()
		ior.ok = ior.prev != 0
	})
	return ior.ok
}

var reVoltageStates = regexp.MustCompile(`"voltage-states(\d+)-sram" = <([0-9a-f]+)>`)

// pmgrFrequencies reads the efficiency- and performance-core frequency tables
// (MHz, ascending). The tables are (frequency, voltage) uint32 pairs; newer
// chips store kHz where older ones store Hz.
func pmgrFrequencies() (ecpu, pcpu []float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-n", "pmgr", "-d", "1", "-w", "0").Output()
	if err != nil {
		return nil, nil
	}
	tables := map[string][]float64{}
	for _, m := range reVoltageStates.FindAllStringSubmatch(string(out), -1) {
		raw, err := hex.DecodeString(m[2])
		if err != nil {
			continue
		}
		var mhz []float64
		for i := 0; i+8 <= len(raw); i += 8 {
			f := float64(binary.LittleEndian.Uint32(raw[i:]))
			if f == 0 {
				continue
			}
			if f > 1e8 {
				f /= 1e6 // Hz
			} else {
				f /= 1e3 // kHz
			}
			mhz = append(mhz, f)
		}
		sort.Float64s(mhz)
		tables[m[1]] = mhz
	}
	return tables["1"], tables["5"]
}

// iorSample returns average CPU and GPU package power (W) and per-core clock
// speeds since the previous call. Must be called from a single goroutine.
func iorSample() (cpuW, gpuW float64, freqs []coreFreq, ok bool) {
	cpuW, gpuW = nan, nan
	if !iorOpen() {
		return
	}
	cur, now := ior.createSamples(ior.sub, ior.channels, 0), time.Now()
	if cur == 0 {
		return
	}
	dt := now.Sub(ior.prevT).Seconds()
	delta := ior.samplesDelta(ior.prev, cur, 0)
	ior.cfRelease(ior.prev)
	ior.prev, ior.prevT = cur, now
	if delta == 0 || dt <= 0 {
		return
	}
	defer ior.cfRelease(delta)

	items := ior.cfDictGetValue(delta, ior.keyChannels)
	if items == 0 {
		return
	}
	nE, nP := 0, 0
	for i, n := 0, ior.cfArrayGetCount(items); i < n; i++ {
		item := ior.cfArrayGetValue(items, i)
		group, name := cfString(ior.chGroup(item)), cfString(ior.chName(item))
		switch {
		case group == "Energy Model":
			isCPU, isGPU := strings.HasSuffix(name, "CPU Energy"), strings.HasSuffix(name, "GPU Energy")
			if !isCPU && !isGPU {
				continue
			}
			joules := float64(ior.simpleInt(item, 0))
			switch cfString(ior.chUnit(item)) {
			case "mJ":
				joules /= 1e3
			case "uJ":
				joules /= 1e6
			case "nJ":
				joules /= 1e9
			default:
				continue
			}
			if isCPU { // multi-die chips report one counter per die
				cpuW = nanAdd(cpuW, joules/dt)
			} else {
				gpuW = nanAdd(gpuW, joules/dt)
			}
		case group == "CPU Stats":
			table, label, perf := ior.pcpuMHz, "", true
			switch {
			case strings.HasPrefix(name, "ECPU"):
				table, perf = ior.ecpuMHz, false
				nE++
				label = "E-core " + strconv.Itoa(nE)
			case strings.HasPrefix(name, "PCPU"):
				nP++
				label = "P-core " + strconv.Itoa(nP)
			default:
				continue
			}
			if len(table) == 0 {
				continue
			}
			freqs = append(freqs, coreFreq{Label: label, MHz: coreMHz(item, table), Max: table[len(table)-1], perf: perf})
		}
	}
	// Efficiency cores first, matching how macOS numbers the CPUs.
	sort.SliceStable(freqs, func(i, j int) bool { return !freqs[i].perf && freqs[j].perf })
	return cpuW, gpuW, freqs, true
}

// coreMHz averages a core's frequency over the time it was actually running.
// The first states (IDLE / DOWN / OFF) are not clocked; the remaining ones map
// one-to-one onto the frequency table.
func coreMHz(item uintptr, table []float64) float64 {
	count := int(ior.stateCount(item))
	offset := count - len(table)
	if offset < 0 {
		return nan
	}
	var total, weighted float64
	for i := offset; i < count; i++ {
		r := float64(ior.stateRes(item, int32(i)))
		total += r
		weighted += r * table[i-offset]
	}
	if total == 0 {
		return table[0] // asleep for the whole interval: parked at the lowest step
	}
	return weighted / total
}

func nanAdd(a, b float64) float64 {
	if a != a {
		return b
	}
	return a + b
}

func platformCPUHW() cpuHW {
	cpuW, gpuW, freqs, _ := iorSample()
	return cpuHW{cpuW: cpuW, gpuW: gpuW, freqs: freqs}
}
