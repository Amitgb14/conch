//go:build linux

package sysstat

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Amitgb14/conch/internal/proto"
)

// Linux says everything in /proc, which is read directly: ps there counts
// processor time in whole seconds, too coarse to tell 30% from 70% over
// two seconds.

// clockTicks is USER_HZ, which is 100 on every Linux conch runs on; the
// kernel fixes it for user space whatever its own HZ.
const clockTicks = 100

// Local is this machine's source.
func Local() Source {
	return Source{
		Host:     linuxHost,
		Ticks:    func() (CPUTicks, bool) { return readFile("/proc/stat", parseProcStat) },
		Memory:   linuxMemory,
		Load:     func() []float64 { b, _ := os.ReadFile("/proc/loadavg"); return parseLoadavg(b) },
		Disk:     homeDisk,
		Procs:    func(ctx context.Context) ([]Proc, error) { return procProcs(ctx, "/proc") },
		Commands: func(_ context.Context, pids []int) map[int]string { return procCommands("/proc", pids) },
	}
}

func readFile[T any](path string, parse func([]byte) (T, bool)) (T, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		var zero T
		return zero, false
	}
	return parse(b)
}

func linuxHost() proto.SystemHost {
	h := proto.SystemHost{Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
	h.Name, _ = os.Hostname()
	var u unix.Utsname
	kernel := ""
	if unix.Uname(&u) == nil {
		kernel = unix.ByteSliceToString(u.Release[:])
	}
	h.OS = strings.TrimSpace("Linux " + kernel)
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		if name := osRelease(b); name != "" {
			h.OS = name + " · " + h.OS
		}
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		h.Model = cpuModel(b)
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		h.Uptime = parseUptime(b)
	}
	return h
}

func linuxMemory(context.Context) (*proto.SystemMemory, bool) {
	m, ok := readFile("/proc/meminfo", parseMeminfo)
	if !ok {
		return nil, false
	}
	if b, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		m.Pressure = parsePSI(b)
	}
	if m.Pressure == "" {
		m.Pressure = pressureByAvailable(m)
	}
	return m, true
}

func homeDisk() *proto.SystemDisk {
	path, err := os.UserHomeDir()
	if err != nil {
		path = "/"
	}
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return nil
	}
	bs := uint64(st.Bsize)
	return &proto.SystemDisk{Path: path, Total: st.Blocks * bs, Free: st.Bavail * bs}
}

var (
	userNamesMu sync.Mutex
	userNames   = map[int]string{}
)

// userName is a uid's name, looked up once; a uid with no name is its
// number.
func userName(uid int) string {
	userNamesMu.Lock()
	defer userNamesMu.Unlock()
	if n, ok := userNames[uid]; ok {
		return n
	}
	n := strconv.Itoa(uid)
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	userNames[uid] = n
	return n
}

// procProcs reads every process under proc. One that ends while it is
// read is skipped.
func procProcs(ctx context.Context, proc string) ([]Proc, error) {
	dirs, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	page := uint64(os.Getpagesize())
	var boot time.Time
	if b, err := os.ReadFile(filepath.Join(proc, "uptime")); err == nil {
		boot = time.Now().Add(-time.Duration(parseUptime(b)) * time.Second)
	}
	var out []Proc
	for _, d := range dirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		st, ok := readFile(filepath.Join(proc, d.Name(), "stat"), parsePidStat)
		if !ok {
			continue
		}
		p := Proc{PID: pid, PPID: st.ppid, Name: st.name, Threads: st.threads,
			CPU: float64(st.utime+st.stime) / clockTicks}
		if st.rssPages > 0 {
			p.RSS = uint64(st.rssPages) * page
		}
		if !boot.IsZero() {
			p.Started = boot.Add(time.Duration(st.start) * time.Second / clockTicks)
		}
		if uid, ok := readFile(filepath.Join(proc, d.Name(), "status"), parseUid); ok {
			p.User = userName(uid)
		}
		out = append(out, p)
	}
	return out, nil
}

func procCommands(proc string, pids []int) map[int]string {
	out := map[int]string{}
	for _, pid := range pids {
		if b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "cmdline")); err == nil {
			if c := parseCmdline(b); c != "" {
				out[pid] = c
			}
		}
	}
	return out
}
