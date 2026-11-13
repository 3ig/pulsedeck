package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func platformTemps() []tempReading { return nil } // generic hwmon path in hw.go is used

// platformFans lists every fan tachometer hwmon exposes (motherboard sensor
// chips such as nct6775/it87, laptops' embedded controllers, AMD GPUs).
func platformFans() []fanReading {
	inputs, _ := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_input")
	var out []fanReading
	for _, in := range inputs {
		rpm := sysNum(in)
		if rpm != rpm {
			continue
		}
		base := strings.TrimSuffix(in, "_input")
		label := "Fan " + strconv.Itoa(len(out)+1)
		if b, err := os.ReadFile(base + "_label"); err == nil && strings.TrimSpace(string(b)) != "" {
			label = strings.TrimSpace(string(b))
		}
		max := sysNum(base + "_max")
		if max != max {
			max = 0
		}
		out = append(out, fanReading{Label: label, RPM: rpm, Max: max})
	}
	return out
}
func platformPower() (float64, bool) { return 0, false }

func sysNum(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return nan
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return nan
	}
	return f
}

// platformGPUs covers AMD (and other drivers exposing gpu_busy_percent) through
// sysfs. NVIDIA cards are handled by nvidia-smi in hw.go.
func platformGPUs() []gpuStat {
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device/gpu_busy_percent")
	var gpus []gpuStat
	for _, busy := range cards {
		dev := filepath.Dir(busy)
		vendor, _ := os.ReadFile(filepath.Join(dev, "vendor"))
		name := "GPU"
		switch strings.TrimSpace(string(vendor)) {
		case "0x1002":
			name = "AMD GPU"
		case "0x8086":
			name = "Intel GPU"
		case "0x10de":
			if nvidiaErr == nil {
				continue
			}
			name = "NVIDIA GPU"
		}
		g := gpuStat{name: name, util: sysNum(busy), temp: nan, power: nan, fan: nan,
			memUsed:  sysNum(filepath.Join(dev, "mem_info_vram_used")),
			memTotal: sysNum(filepath.Join(dev, "mem_info_vram_total"))}
		if t, _ := filepath.Glob(filepath.Join(dev, "hwmon/hwmon*/temp1_input")); len(t) > 0 {
			g.temp = sysNum(t[0]) / 1000
		}
		if p, _ := filepath.Glob(filepath.Join(dev, "hwmon/hwmon*/power1_average")); len(p) > 0 {
			g.power = sysNum(p[0]) / 1e6
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func platformMemUsed() (uint64, bool) { return 0, false } // gopsutil's figure already excludes cache

var rapl struct {
	prevUJ float64
	prevT  time.Time
}

// platformCPUHW reads per-core clock speed from cpufreq and package power from
// the RAPL energy counter (readable without root only on some kernels).
func platformCPUHW() cpuHW {
	hw := cpuHW{cpuW: nan, gpuW: nan}
	for i := 0; ; i++ {
		base := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/", i)
		khz := sysNum(base + "scaling_cur_freq")
		if khz != khz {
			break
		}
		max := sysNum(base + "cpuinfo_max_freq")
		if max != max {
			max = 0
		}
		hw.freqs = append(hw.freqs, coreFreq{Label: "CPU " + strconv.Itoa(i+1), MHz: khz / 1000, Max: max / 1000})
	}
	if uj, now := sysNum("/sys/class/powercap/intel-rapl:0/energy_uj"), time.Now(); uj == uj {
		if !rapl.prevT.IsZero() && uj >= rapl.prevUJ {
			hw.cpuW = (uj - rapl.prevUJ) / 1e6 / now.Sub(rapl.prevT).Seconds()
		} else if rapl.prevT.IsZero() {
			hw.cpuW = 0
		}
		rapl.prevUJ, rapl.prevT = uj, now
	}
	return hw
}
