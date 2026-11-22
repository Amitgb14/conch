package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Memory is the whole computer's memory, in bytes.
//
// Used is memory nothing else can have without something being pushed out:
// on Linux MemTotal - MemAvailable, which is what free(1) calls used; on
// macOS app memory (anonymous pages less purgeable ones) + wired + what the
// compressor occupies, which is Activity Monitor's "Memory Used". Neither
// counts the file cache, which the system gives back as it is needed.
type Memory struct {
	Total, Used int64
}

// Ticks is the whole computer's CPU time since boot, in the kernel's
// ticks: two of them taken apart say how busy it was in between.
type Ticks struct {
	Busy, Total uint64
}

// ReadMemory reads the computer's memory: /proc/meminfo on Linux, sysctl
// and vm_stat on macOS.
func ReadMemory() (Memory, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return Memory{}, err
		}
		return ParseMeminfo(b)
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		size, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return Memory{}, fmt.Errorf("sysctl hw.memsize: %w", err)
		}
		total, err := strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
		if err != nil || total <= 0 {
			return Memory{}, fmt.Errorf("sysctl hw.memsize: %q", strings.TrimSpace(string(size)))
		}
		vm, err := exec.CommandContext(ctx, "vm_stat").Output()
		if err != nil {
			return Memory{}, fmt.Errorf("vm_stat: %w", err)
		}
		return ParseVMStat(vm, total)
	}
	return Memory{}, fmt.Errorf("memory is not read on %s", runtime.GOOS)
}

// ReadTicks reads the computer's CPU time from /proc/stat. Only Linux
// keeps one there; elsewhere ok is false and CPU use is worked out from
// the processes instead.
func ReadTicks() (Ticks, bool) {
	if runtime.GOOS != "linux" {
		return Ticks{}, false
	}
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return Ticks{}, false
	}
	return ParseProcStat(b)
}

// ParseMeminfo reads /proc/meminfo. A kernel older than MemAvailable
// (3.14) is given free + buffers + cache as available.
func ParseMeminfo(b []byte) (Memory, error) {
	kb := map[string]int64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil && n >= 0 {
			kb[name] = n
		}
	}
	total, ok := kb["MemTotal"]
	if !ok || total == 0 {
		return Memory{}, fmt.Errorf("/proc/meminfo has no MemTotal")
	}
	avail, ok := kb["MemAvailable"]
	if !ok {
		avail = kb["MemFree"] + kb["Buffers"] + kb["Cached"]
	}
	used := min(max(total-avail, 0), total)
	return Memory{Total: total * 1024, Used: used * 1024}, nil
}

// ParseVMStat reads vm_stat's pages with total, from hw.memsize: used is
// app memory + wired + compressed, as Activity Monitor adds it up. An
// older vm_stat without anonymous pages counts active ones as the apps'.
func ParseVMStat(b []byte, total int64) (Memory, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	page := int64(0)
	pages := map[string]int64{}
	for sc.Scan() {
		line := sc.Text()
		if _, rest, ok := strings.Cut(line, "page size of "); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				page, _ = strconv.ParseInt(f[0], 10, 64)
			}
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), "."), 10, 64); err == nil && n >= 0 {
			pages[strings.Trim(name, `"`)] = n
		}
	}
	if page <= 0 {
		return Memory{}, fmt.Errorf("vm_stat gave no page size")
	}
	wired, ok := pages["Pages wired down"]
	if !ok {
		return Memory{}, fmt.Errorf("vm_stat gave no wired pages")
	}
	app, ok := pages["Anonymous pages"]
	if ok {
		app = max(app-pages["Pages purgeable"], 0)
	} else {
		app = pages["Pages active"]
	}
	used := (app + wired + pages["Pages occupied by compressor"]) * page
	return Memory{Total: total, Used: min(used, total)}, nil
}

// ParseProcStat reads the first line of /proc/stat: user nice system idle
// iowait irq softirq steal, then guest time, which user already counts.
// Idle and waiting for disk are not busy.
func ParseProcStat(b []byte) (Ticks, bool) {
	line, _, _ := bytes.Cut(b, []byte("\n"))
	f := strings.Fields(string(line))
	if len(f) < 5 || f[0] != "cpu" {
		return Ticks{}, false
	}
	var t Ticks
	for i, s := range f[1:min(len(f), 9)] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return Ticks{}, false
		}
		t.Total += n
		if i != 3 && i != 4 { // idle, iowait
			t.Busy += n
		}
	}
	return t, t.Total > 0
}

// BusyPercent is how busy the computer was between two readings, 0–100.
func BusyPercent(prev, cur Ticks) (float64, bool) {
	if cur.Total <= prev.Total || cur.Busy < prev.Busy {
		return 0, false
	}
	pct := float64(cur.Busy-prev.Busy) / float64(cur.Total-prev.Total) * 100
	return min(max(pct, 0), 100), true
}
