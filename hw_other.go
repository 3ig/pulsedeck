//go:build !darwin && !linux && !windows

package main

func platformTemps() []tempReading    { return nil }
func platformFans() []fanReading      { return nil }
func platformPower() (float64, bool)  { return 0, false }
func platformGPUs() []gpuStat         { return nil }
func platformCPUHW() cpuHW            { return cpuHW{cpuW: nan, gpuW: nan} }
func platformMemUsed() (uint64, bool) { return 0, false }
