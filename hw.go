package main

// Hardware readings that need per-OS sources: temperatures, fans, power and
// GPUs. Each OS file provides platformTemps/Fans/Power/GPUs; this file holds
// the shared types, the portable fallbacks and NVIDIA support.

import (
	"context"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/sensors"
)

var nan = math.NaN()

type tempReading struct {
	Key   string
	Label string
	Kind  string // cpu | gpu | ssd | battery | "" (other)
	C     float64
	// Hidden readings only feed the aggregates (CPU, GPU, hottest…). Used
	// where a machine exposes hundreds of cryptically named sensors.
	Hidden bool
}

type fanReading struct {
	Label    string
	RPM, Max float64
}

type coreFreq struct {
	Label    string
	MHz, Max float64
	perf     bool
}

// cpuHW is what the platform knows about the processor beyond utilisation.
// Power figures are NaN when unavailable. Sampled from one goroutine only:
// implementations keep the previous counters to compute rates.
type cpuHW struct {
	cpuW, gpuW float64
	freqs      []coreFreq
}

// extraReading is a sensor that comes ready-made from another monitoring
// program (HWiNFO on Windows), published as-is under its own group.
type extraReading struct {
	ID, Label, Group, Unit string
	Value, Min, Max        float64
}

// gpuStat fields are NaN when the driver does not report them.
type gpuStat struct {
	name                                      string
	util, temp, memUsed, memTotal, power, fan float64
}

func tempKind(key string) string {
	k := strings.ToLower(key)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(k, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("gpu", "amdgpu", "radeon", "nouveau"):
		return "gpu"
	case has("coretemp", "k10temp", "zenpower", "cpu", "tdie", "package", "core", "pacc", "eacc", "soc"):
		return "cpu"
	case has("nvme", "nand", "ssd", "drivetemp"):
		return "ssd"
	case has("bat", "gas gauge"):
		return "battery"
	}
	return ""
}

var tempChips = map[string]string{
	"k10temp": "CPU", "coretemp": "CPU", "zenpower": "CPU", "cpu_thermal": "CPU",
	"nvme": "NVMe", "amdgpu": "GPU", "radeon": "GPU", "nouveau": "GPU",
	"acpitz": "Motherboard", "iwlwifi_1": "Wi-Fi", "iwlwifi": "Wi-Fi",
}

// tempLabel turns a hwmon-style key ("k10temp_tctl", "nvme_composite") into
// something readable ("CPU Tctl", "NVMe composite").
func tempLabel(key string, nth int) string {
	chip, rest := "", ""
	for name := range tempChips { // longest matching chip name wins
		if strings.HasPrefix(key, name) && len(name) > len(chip) {
			chip, rest = name, strings.Trim(strings.TrimPrefix(key, name), "_ ")
		}
	}
	label, known := tempChips[chip]
	if !known {
		return key
	}
	if nth > 1 {
		label += " " + strconv.Itoa(nth)
	}
	if rest != "" {
		label += " " + strings.ReplaceAll(rest, "_", " ")
	}
	return label
}

// readTemps prefers the platform's native source and falls back to what
// gopsutil can see (hwmon on Linux, HID sensors on macOS, ACPI on Windows).
func readTemps() []tempReading {
	if t := platformTemps(); len(t) > 0 {
		return t
	}
	all, _ := sensors.SensorsTemperatures() // partial results come with a warning error
	out := make([]tempReading, 0, len(all))
	seen := map[string]int{}
	for _, t := range all {
		// Two identical devices (a second NVMe drive, say) report identical
		// keys; number them so both stay visible. The order is stable.
		seen[t.SensorKey]++
		key, nth := t.SensorKey, seen[t.SensorKey]
		if nth > 1 {
			key += "_" + strconv.Itoa(nth)
		}
		if t.Temperature > 5 && t.Temperature < 130 {
			out = append(out, tempReading{Key: key, Label: tempLabel(t.SensorKey, nth), Kind: tempKind(t.SensorKey), C: t.Temperature})
		}
	}
	return out
}

func readGPUs() []gpuStat    { return append(readNvidia(), platformGPUs()...) }
func readFans() []fanReading { return platformFans() }

var nvidiaPath, nvidiaErr = exec.LookPath("nvidia-smi")

func readNvidia() []gpuStat {
	if nvidiaErr != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := quiet(exec.CommandContext(ctx, nvidiaPath,
		"--query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total,power.draw,fan.speed",
		"--format=csv,noheader,nounits")).Output()
	if err != nil {
		return nil
	}
	var gpus []gpuStat
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 7 {
			continue
		}
		num := func(i int) float64 { // "[N/A]" on cards without that sensor
			n, err := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
			if err != nil {
				return nan
			}
			return n
		}
		const mib = 1024 * 1024
		gpus = append(gpus, gpuStat{strings.TrimSpace(f[0]), num(1), num(2), num(3) * mib, num(4) * mib, num(5), num(6)})
	}
	return gpus
}
