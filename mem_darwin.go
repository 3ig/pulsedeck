package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var (
	reVMPageSize = regexp.MustCompile(`page size of (\d+) bytes`)
	reVMStat     = regexp.MustCompile(`(?m)^(?:Pages )?"?([A-Za-z -]+?)"?:\s+(\d+)\.`)
)

// platformMemUsed reports memory in use the way Activity Monitor does:
// app memory (anonymous pages that are not purgeable) + wired + compressed.
// File cache, which macOS gives back on demand, is not counted.
func platformMemUsed() (uint64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, false
	}
	text := string(out)
	ps := reVMPageSize.FindStringSubmatch(text)
	if ps == nil {
		return 0, false
	}
	pageSize, _ := strconv.ParseUint(ps[1], 10, 64)
	pages := map[string]uint64{}
	for _, m := range reVMStat.FindAllStringSubmatch(text, -1) {
		pages[m[1]], _ = strconv.ParseUint(m[2], 10, 64)
	}
	anon, wired, compressed := pages["Anonymous pages"], pages["wired down"], pages["occupied by compressor"]
	if anon == 0 || wired == 0 {
		return 0, false
	}
	app := anon
	if p := pages["purgeable"]; p < anon {
		app = anon - p
	}
	return (app + wired + compressed) * pageSize, true
}
