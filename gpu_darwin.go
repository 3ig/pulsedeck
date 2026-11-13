package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

var (
	reGPUModel = regexp.MustCompile(`"model" = "([^"]+)"`)
	reGPUUtil  = regexp.MustCompile(`"Device Utilization %"=(\d+)`)
	reGPUInUse = regexp.MustCompile(`"In use system memory"=(\d+)`)
	reGPUVRAM  = regexp.MustCompile(`"VRAM,totalMB" = (\d+)`)
)

// platformGPUs reads the GPU driver's own statistics from the I/O Registry,
// which needs no elevated privileges (unlike powermetrics).
func platformGPUs() []gpuStat {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator").Output()
	if err != nil {
		return nil
	}
	gpuTemp := nan
	if temps := platformTemps(); len(temps) > 0 {
		sum, n := 0.0, 0
		for _, t := range temps {
			if t.Kind == "gpu" {
				sum += t.C
				n++
			}
		}
		if n > 0 {
			gpuTemp = sum / float64(n)
		}
	}

	var gpus []gpuStat
	for _, block := range strings.Split(string(out), "+-o ")[1:] {
		m := reGPUUtil.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		g := gpuStat{name: "GPU", util: atof(m[1]), temp: gpuTemp, memUsed: nan, memTotal: nan, power: nan, fan: nan}
		if m := reGPUModel.FindStringSubmatch(block); m != nil {
			g.name = m[1]
		}
		if m := reGPUInUse.FindStringSubmatch(block); m != nil {
			g.memUsed = atof(m[1])
			if v := reGPUVRAM.FindStringSubmatch(block); v != nil {
				g.memTotal = atof(v[1]) * 1024 * 1024
			} else if vm, err := mem.VirtualMemory(); err == nil {
				// Apple silicon: the GPU shares system memory, so usage is
				// reported against total RAM.
				g.memTotal = float64(vm.Total)
			}
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
