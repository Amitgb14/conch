package sysstat

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// The readers of what each system prints, kept apart from the reading so
// every one of them is tested on every platform: a Linux parser broken on
// a Mac developer's machine would otherwise be found by CI or not at all.

// Ticks is time a processor spent, in whatever unit its system counts:
// only differences between two readings mean anything.
type Ticks struct {
	Busy, Idle uint64
	User, Sys  uint64 // parts of Busy, where the system says
}

// CPUTicks is the whole machine's and each core's.
type CPUTicks struct {
	All   Ticks
	Cores []Ticks
}

// share is how busy a processor was between two readings, 0–100, with
// the user and system parts.
func share(was, now Ticks) (total, user, sys float64, ok bool) {
	if now.Busy < was.Busy || now.Idle < was.Idle {
		return 0, 0, 0, false // a counter went back: a reboot, a wrap
	}
	busy, idle := now.Busy-was.Busy, now.Idle-was.Idle
	if busy+idle == 0 {
		return 0, 0, 0, false
	}
	all := float64(busy + idle)
	total = float64(busy) / all * 100
	if now.User >= was.User && now.Sys >= was.Sys {
		user = float64(now.User-was.User) / all * 100
		sys = float64(now.Sys-was.Sys) / all * 100
	}
	return total, user, sys, true
}

// parseProcStat reads Linux's /proc/stat: the "cpu" line for the machine
// and "cpuN" for each core — user nice system idle iowait irq softirq
// steal, in clock ticks. Waiting on a disk counts as idle, as top has it.
func parseProcStat(b []byte) (CPUTicks, bool) {
	var out CPUTicks
	found := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var v [8]uint64
		bad := false
		for i := 1; i < len(f) && i <= 8; i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				bad = true
				break
			}
			v[i-1] = n
		}
		if bad {
			continue
		}
		t := Ticks{User: v[0] + v[1], Sys: v[2] + v[5] + v[6], Idle: v[3] + v[4]}
		t.Busy = t.User + t.Sys + v[7]
		if f[0] == "cpu" {
			out.All, found = t, true
		} else if _, err := strconv.Atoi(f[0][3:]); err == nil {
			out.Cores = append(out.Cores, t)
		}
	}
	return out, found
}

// parseMeminfo reads Linux's /proc/meminfo (kB) into memory: used is what
// is not available, cached the page cache the kernel can drop.
func parseMeminfo(b []byte) (*proto.SystemMemory, bool) {
	kb := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			kb[k] = n * 1024
		}
	}
	total, ok := kb["MemTotal"]
	if !ok || total == 0 {
		return nil, false
	}
	avail, ok := kb["MemAvailable"]
	if !ok { // kernels before 3.14
		avail = kb["MemFree"] + kb["Buffers"] + kb["Cached"]
	}
	avail = min(avail, total)
	m := &proto.SystemMemory{Total: total, Used: total - avail, Free: kb["MemFree"]}
	cached := kb["Buffers"] + kb["Cached"] + kb["SReclaimable"]
	if sh := kb["Shmem"]; sh < cached {
		cached -= sh
	}
	m.Cached = min(cached, total-m.Used)
	m.Apps = m.Used
	if st := kb["SwapTotal"]; st > 0 {
		m.SwapTotal = st
		m.SwapUsed = st - min(kb["SwapFree"], st)
	}
	return m, true
}

// parsePSI reads /proc/pressure/memory: how much of the last ten seconds
// some task waited on memory. Ten percent is a machine that feels it.
func parsePSI(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				n, err := strconv.ParseFloat(v, 64)
				switch {
				case err != nil:
					return ""
				case n >= 30:
					return proto.PressureCritical
				case n >= 10:
					return proto.PressureWarn
				default:
					return proto.PressureNormal
				}
			}
		}
	}
	return ""
}

// pressureByAvailable is a guess where the kernel keeps no PSI: a machine
// with under a tenth of its memory available is short of it.
func pressureByAvailable(m *proto.SystemMemory) string {
	if m == nil || m.Total == 0 {
		return ""
	}
	free := float64(m.Total-m.Used) / float64(m.Total)
	switch {
	case free < 0.05:
		return proto.PressureCritical
	case free < 0.10:
		return proto.PressureWarn
	}
	return proto.PressureNormal
}

// parseLoadavg reads /proc/loadavg's first three fields.
func parseLoadavg(b []byte) []float64 {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// parseUptime reads /proc/uptime's first field, in whole seconds.
func parseUptime(b []byte) int64 {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil || n < 0 {
		return 0
	}
	return int64(n)
}

// pidStat is what /proc/PID/stat says of a process.
type pidStat struct {
	name         string
	ppid         int
	utime, stime uint64 // clock ticks
	threads      int
	start        uint64 // clock ticks after boot
	rssPages     int64
}

// parsePidStat reads /proc/PID/stat. The name is in parentheses and may
// hold spaces and parentheses itself, so the fields are counted from the
// last ")".
func parsePidStat(b []byte) (pidStat, bool) {
	s := string(b)
	open, shut := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || shut < open {
		return pidStat{}, false
	}
	f := strings.Fields(s[shut+1:])
	// state ppid pgrp session tty tpgid flags minflt cminflt majflt
	// cmajflt utime stime cutime cstime priority nice threads itreal
	// starttime vsize rss
	if len(f) < 22 {
		return pidStat{}, false
	}
	var st pidStat
	var err error
	st.name = s[open+1 : shut]
	if st.ppid, err = strconv.Atoi(f[1]); err != nil {
		return pidStat{}, false
	}
	if st.utime, err = strconv.ParseUint(f[11], 10, 64); err != nil {
		return pidStat{}, false
	}
	if st.stime, err = strconv.ParseUint(f[12], 10, 64); err != nil {
		return pidStat{}, false
	}
	st.threads, _ = strconv.Atoi(f[17])
	st.start, _ = strconv.ParseUint(f[19], 10, 64)
	st.rssPages, _ = strconv.ParseInt(f[21], 10, 64)
	return st, true
}

// parseUid reads the real uid off /proc/PID/status.
func parseUid(b []byte) (int, bool) {
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Uid:"); ok {
			f := strings.Fields(v)
			if len(f) == 0 {
				return 0, false
			}
			n, err := strconv.Atoi(f[0])
			return n, err == nil
		}
	}
	return 0, false
}

// parseCmdline turns /proc/PID/cmdline's NUL-separated words into one
// line. A process that rewrote its title into one long string reads the
// same either way.
func parseCmdline(b []byte) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimRight(string(b), "\x00"), "\x00", " "))
}

// parseVMStat reads macOS's vm_stat into memory, as Activity Monitor
// divides it: app memory (anonymous pages not purgeable), wired, the
// compressor's, and cached files (file-backed plus purgeable), with free
// the rest of total.
func parseVMStat(b []byte, total uint64) (*proto.SystemMemory, bool) {
	page := uint64(4096)
	pages := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "page size of "); i >= 0 {
			if f := strings.Fields(line[i+len("page size of "):]); len(f) > 0 {
				if n, err := strconv.ParseUint(f[0], 10, 64); err == nil && n > 0 {
					page = n
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
		if err != nil {
			continue
		}
		pages[strings.TrimSpace(k)] = n
	}
	anon, ok1 := pages["Anonymous pages"]
	wired, ok2 := pages["Pages wired down"]
	if !ok1 || !ok2 || total == 0 {
		return nil, false
	}
	purge := pages["Pages purgeable"]
	m := &proto.SystemMemory{Total: total}
	m.Apps = (anon - min(purge, anon)) * page
	m.Wired = wired * page
	m.Compressed = pages["Pages occupied by compressor"] * page
	m.Cached = (pages["File-backed pages"] + purge) * page
	m.Used = min(m.Apps+m.Wired+m.Compressed, total)
	m.Cached = min(m.Cached, total-m.Used)
	m.Free = total - m.Used - m.Cached
	return m, true
}

// parseLoadavgSysctl reads macOS's vm.loadavg: three fixed-point loads
// (uint32) and their scale (a long, after padding to eight bytes).
func parseLoadavgSysctl(b []byte) []float64 {
	if len(b) < 24 {
		return nil
	}
	scale := binary.LittleEndian.Uint64(b[16:24])
	if scale == 0 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		out[i] = float64(binary.LittleEndian.Uint32(b[4*i:])) / float64(scale)
	}
	return out
}

// parseSwapSysctl reads macOS's vm.swapusage: total, available, used.
func parseSwapSysctl(b []byte) (total, used uint64, ok bool) {
	if len(b) < 24 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint64(b[0:8]), binary.LittleEndian.Uint64(b[16:24]), true
}

// pressureLevel is macOS's kern.memorystatus_vm_pressure_level.
func pressureLevel(n uint32) string {
	switch n {
	case 1:
		return proto.PressureNormal
	case 2:
		return proto.PressureWarn
	case 4:
		return proto.PressureCritical
	}
	return ""
}

// psLine is one process as ps lists it.
type psLine struct {
	pid, ppid int
	rss       uint64 // bytes
	cpu       float64
	elapsed   time.Duration
	user      string
	comm      string
}

// psArgs are the fields read from ps, the same in BSD ps and procps; comm
// last because it may hold spaces.
var psArgs = []string{"-A", "-o", "pid=,ppid=,rss=,time=,etime=,user=,comm="}

// parsePS reads `ps -o pid=,ppid=,rss=,time=,etime=,user=,comm=`, skipping
// a line it cannot read.
func parsePS(b []byte) []psLine {
	var out []psLine
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 7 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseUint(f[2], 10, 64)
		cpu, ok1 := parseClock(f[3])
		el, ok2 := parseClock(f[4])
		if err1 != nil || err2 != nil || err3 != nil || !ok1 || !ok2 {
			continue
		}
		out = append(out, psLine{pid: pid, ppid: ppid, rss: rss * 1024, cpu: cpu,
			elapsed: time.Duration(el * float64(time.Second)), user: f[5], comm: strings.Join(f[6:], " ")})
	}
	return out
}

// parseClock reads ps's [DD-][HH:]MM:SS[.ss] in seconds.
func parseClock(s string) (float64, bool) {
	days := 0.0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = float64(n), rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 {
		return 0, false
	}
	total, mult := sec+days*86400, 60.0
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return 0, false
		}
		total += float64(n) * mult
		mult *= 60
	}
	return total, true
}

// parseArgs reads `ps -o pid=,args=`.
func parseArgs(b []byte) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		p, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(p); err == nil {
			out[pid] = strings.TrimSpace(rest)
		}
	}
	return out
}

// baseName is a process's name from its path: macOS's ps gives comm as
// the whole path of the program.
func baseName(comm string) string {
	if i := strings.LastIndexByte(comm, '/'); i >= 0 && i < len(comm)-1 {
		return comm[i+1:]
	}
	return comm
}

// osRelease is /etc/os-release's PRETTY_NAME: "Ubuntu 24.04.1 LTS".
func osRelease(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// cpuModel is /proc/cpuinfo's first "model name" — or, on ARM, where
// there is none, nothing rather than a guess.
func cpuModel(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "model name" {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

// machTicks turns the Mach host's per-core user, system, idle and nice
// counters (four to a core) into ticks, the machine's the sum. The
// counters are 32 bits and wrap; share treats a wrapped one as no reading.
func machTicks(raw []uint32) CPUTicks {
	var out CPUTicks
	for i := 0; i+3 < len(raw); i += 4 {
		user := uint64(raw[i]) + uint64(raw[i+3])
		sys := uint64(raw[i+1])
		t := Ticks{User: user, Sys: sys, Busy: user + sys, Idle: uint64(raw[i+2])}
		out.Cores = append(out.Cores, t)
		out.All.User += t.User
		out.All.Sys += t.Sys
		out.All.Busy += t.Busy
		out.All.Idle += t.Idle
	}
	return out
}
