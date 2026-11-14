//go:build darwin

package sysstat

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Amitgb14/conch/internal/proto"
)

// Local is this machine's source. Each core's ticks need the Mach host
// interface (cgo); without it the CPU is reckoned from the processes'
// own time, the machine as a whole and not split.
func Local() Source {
	return Source{
		Host:     darwinHost,
		Ticks:    darwinTicks,
		Memory:   darwinMemory,
		Load:     func() []float64 { b, _ := unix.SysctlRaw("vm.loadavg"); return parseLoadavgSysctl(b) },
		Disk:     homeDisk,
		Procs:    psProcs,
		Commands: psCommands,
	}
}

func darwinHost() proto.SystemHost {
	h := proto.SystemHost{Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
	h.Name, _ = os.Hostname()
	h.Name = strings.TrimSuffix(h.Name, ".local")
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		h.OS = "macOS " + v
	} else {
		h.OS = "macOS"
	}
	h.Model, _ = unix.Sysctl("machdep.cpu.brand_string")
	// Apple silicon: level 0 is the performance cores, level 1 efficiency.
	if n, err := unix.SysctlUint32("hw.perflevel0.logicalcpu"); err == nil {
		if e, err := unix.SysctlUint32("hw.perflevel1.logicalcpu"); err == nil && e > 0 {
			h.PCores, h.ECores = int(n), int(e)
		}
	}
	if tv, err := unix.SysctlTimeval("kern.boottime"); err == nil && tv.Sec > 0 {
		h.Uptime = int64(time.Since(time.Unix(tv.Sec, 0)).Seconds())
	}
	return h
}

func darwinMemory(ctx context.Context) (*proto.SystemMemory, bool) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return nil, false
	}
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return nil, false
	}
	m, ok := parseVMStat(out, total)
	if !ok {
		return nil, false
	}
	if b, err := unix.SysctlRaw("vm.swapusage"); err == nil {
		if t, u, ok := parseSwapSysctl(b); ok {
			m.SwapTotal, m.SwapUsed = t, u
		}
	}
	if n, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		m.Pressure = pressureLevel(n)
	}
	return m, true
}

// homeDisk is the volume the home directory is on: on macOS "/" is the
// sealed system volume, whose free space is the data volume's anyway, but
// whose size is not.
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
