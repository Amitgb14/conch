package procs

import (
	"runtime"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	m, err := ParseMeminfo([]byte("MemTotal:       16000000 kB\nMemFree:  1000000 kB\nMemAvailable:    6000000 kB\nBuffers: 1 kB\nbogus\nCached:\n"))
	if err != nil || m.Total != 16000000*1024 || m.Used != 10000000*1024 {
		t.Errorf("meminfo: %+v %v", m, err)
	}
	// A kernel before MemAvailable.
	m, err = ParseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 250 kB\n"))
	if err != nil || m.Total != 1000*1024 || m.Used != 600*1024 {
		t.Errorf("old kernel: %+v %v", m, err)
	}
	// More available than there is (a container's numbers) is never negative.
	m, err = ParseMeminfo([]byte("MemTotal: 1000 kB\nMemAvailable: 5000 kB\n"))
	if err != nil || m.Used != 0 {
		t.Errorf("overstated available: %+v %v", m, err)
	}
	for _, bad := range []string{"", "MemFree: 1 kB\n", "MemTotal: 0 kB\n", "MemTotal: x kB\n"} {
		if m, err := ParseMeminfo([]byte(bad)); err == nil {
			t.Errorf("%q: %+v", bad, m)
		}
	}
}

const vmStat = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                4853.
Pages active:                            162970.
Pages inactive:                          150222.
Pages speculative:                        12039.
Pages wired down:                        240949.
Pages purgeable:                              4.
"Translation faults":              117504330720.
File-backed pages:                       108008.
Anonymous pages:                         217223.
Pages occupied by compressor:            435791.
`

func TestParseVMStat(t *testing.T) {
	total := int64(17179869184)
	m, err := ParseVMStat([]byte(vmStat), total)
	want := (217223 - 4 + 240949 + 435791) * int64(16384)
	if err != nil || m.Total != total || m.Used != want {
		t.Errorf("vm_stat: %+v %v, want used %d", m, err, want)
	}
	// An older vm_stat without anonymous pages counts active ones.
	m, err = ParseVMStat([]byte("Mach Virtual Memory Statistics: (page size of 4096 bytes)\nPages active: 10.\nPages wired down: 5.\n"), 1<<30)
	if err != nil || m.Used != 15*4096 {
		t.Errorf("older vm_stat: %+v %v", m, err)
	}
	// Never more used than there is.
	if m, _ := ParseVMStat([]byte(vmStat), 1024); m.Used != 1024 {
		t.Errorf("capped: %+v", m)
	}
	for _, bad := range []string{"", "Pages wired down: 5.\n", "Mach (page size of 4096 bytes)\nPages active: 1.\n"} {
		if m, err := ParseVMStat([]byte(bad), 1<<30); err == nil {
			t.Errorf("%q: %+v", bad, m)
		}
	}
}

func TestParseProcStat(t *testing.T) {
	ticks, ok := ParseProcStat([]byte("cpu  100 10 50 800 40 0 0 0 7 0\ncpu0 1 2 3 4\n"))
	if !ok || ticks.Total != 1000 || ticks.Busy != 160 {
		t.Errorf("stat: %+v %v", ticks, ok)
	}
	// An old kernel with only four columns.
	if ticks, ok := ParseProcStat([]byte("cpu 1 2 3 4\n")); !ok || ticks.Total != 10 || ticks.Busy != 6 {
		t.Errorf("four columns: %+v %v", ticks, ok)
	}
	for _, bad := range []string{"", "cpu0 1 2 3 4 5\n", "cpu 1 2\n", "cpu 1 x 3 4 5\n", "cpu 0 0 0 0 0\n"} {
		if ticks, ok := ParseProcStat([]byte(bad)); ok {
			t.Errorf("%q: %+v", bad, ticks)
		}
	}
}

func TestBusyPercent(t *testing.T) {
	if pct, ok := BusyPercent(Ticks{Busy: 100, Total: 1000}, Ticks{Busy: 350, Total: 2000}); !ok || pct != 25 {
		t.Errorf("busy %v %v", pct, ok)
	}
	for _, c := range [][2]Ticks{
		{{Busy: 1, Total: 10}, {Busy: 1, Total: 10}}, // no time passed
		{{Busy: 5, Total: 10}, {Busy: 4, Total: 20}}, // counters went back (a reboot)
		{{Busy: 5, Total: 20}, {Busy: 6, Total: 10}},
	} {
		if pct, ok := BusyPercent(c[0], c[1]); ok {
			t.Errorf("%+v: %v", c, pct)
		}
	}
}

// The real readers, read-only: their formats must parse where conch runs.
func TestReadMachine(t *testing.T) {
	m, err := ReadMemory()
	switch runtime.GOOS {
	case "linux", "darwin":
		if err != nil || m.Total <= 0 || m.Used <= 0 || m.Used > m.Total {
			t.Fatalf("memory %+v %v", m, err)
		}
	default:
		if err == nil {
			t.Errorf("memory read on %s: %+v", runtime.GOOS, m)
		}
	}
	ticks, ok := ReadTicks()
	if ok != (runtime.GOOS == "linux") || ok && ticks.Busy > ticks.Total {
		t.Errorf("ticks %+v %v on %s", ticks, ok, runtime.GOOS)
	}
}
