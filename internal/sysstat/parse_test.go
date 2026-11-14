package sysstat

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParseProcStat(t *testing.T) {
	in := "cpu  100 10 50 800 40 5 5 0 0 0\n" +
		"cpu0 60 0 30 400 20 0 0 0 0 0\n" +
		"cpu1 40 10 20 400 20 5 5 0 0 0\n" +
		"cpux 1 2 3 4\n" + // not a core
		"cpu2 a b c d\n" + // unreadable
		"intr 12345\n" +
		"ctxt 99\n"
	got, ok := parseProcStat([]byte(in))
	if !ok {
		t.Fatal("no cpu line found")
	}
	if got.All.User != 110 || got.All.Sys != 60 || got.All.Idle != 840 || got.All.Busy != 170 {
		t.Errorf("all = %+v", got.All)
	}
	if len(got.Cores) != 2 {
		t.Fatalf("cores = %d, want 2", len(got.Cores))
	}
	if got.Cores[1].Busy != 80 {
		t.Errorf("core 1 busy = %d", got.Cores[1].Busy)
	}
	for _, bad := range []string{"", "intr 1\n", "cpu 1 2\n"} {
		if _, ok := parseProcStat([]byte(bad)); ok {
			t.Errorf("%q: read as a cpu line", bad)
		}
	}
}

func TestShare(t *testing.T) {
	was := Ticks{Busy: 100, Idle: 100, User: 70, Sys: 30}
	total, user, sys, ok := share(was, Ticks{Busy: 150, Idle: 150, User: 110, Sys: 40})
	if !ok || !near(total, 50) || !near(user, 40) || !near(sys, 10) {
		t.Errorf("share = %v %v %v %v", total, user, sys, ok)
	}
	if _, _, _, ok := share(was, was); ok {
		t.Error("no time passed: a share")
	}
	if _, _, _, ok := share(was, Ticks{Busy: 50, Idle: 300}); ok {
		t.Error("a counter that went back: a share")
	}
	// No split given: the total alone.
	total, user, _, ok = share(Ticks{Busy: 0, Idle: 0}, Ticks{Busy: 25, Idle: 75})
	if !ok || !near(total, 25) || user != 0 {
		t.Errorf("unsplit = %v %v", total, user)
	}
}

func TestParseMeminfo(t *testing.T) {
	in := "MemTotal:       8000000 kB\nMemFree:        1000000 kB\nMemAvailable:   5000000 kB\n" +
		"Buffers:          200000 kB\nCached:          2000000 kB\nSReclaimable:     300000 kB\nShmem: 100000 kB\n" +
		"SwapTotal:      2000000 kB\nSwapFree:       1500000 kB\nbad line\nHugePages_Total: 0\n"
	m, ok := parseMeminfo([]byte(in))
	if !ok {
		t.Fatal("not read")
	}
	k := uint64(1024)
	if m.Total != 8000000*k || m.Used != 3000000*k || m.Free != 1000000*k {
		t.Errorf("total/used/free = %d %d %d", m.Total, m.Used, m.Free)
	}
	if m.Cached != 2400000*k {
		t.Errorf("cached = %d", m.Cached/k)
	}
	if m.SwapTotal != 2000000*k || m.SwapUsed != 500000*k {
		t.Errorf("swap = %d %d", m.SwapTotal, m.SwapUsed)
	}

	// An old kernel: no MemAvailable.
	m, ok = parseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 100 kB\nCached: 200 kB\n"))
	if !ok || m.Used != 600*k {
		t.Errorf("without MemAvailable: used = %d", m.Used/k)
	}
	// Available past total, swap free past total: clamped, not wrapped.
	m, ok = parseMeminfo([]byte("MemTotal: 1000 kB\nMemAvailable: 5000 kB\nSwapTotal: 10 kB\nSwapFree: 20 kB\n"))
	if !ok || m.Used != 0 || m.SwapUsed != 0 {
		t.Errorf("clamping: used %d swap %d", m.Used, m.SwapUsed)
	}
	for _, bad := range []string{"", "MemTotal: 0 kB\n", "MemFree: 10 kB\n"} {
		if _, ok := parseMeminfo([]byte(bad)); ok {
			t.Errorf("%q read", bad)
		}
	}
}

func TestPressure(t *testing.T) {
	for in, want := range map[string]string{
		"some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00\n": proto.PressureNormal,
		"some avg10=12.50 avg60=1 avg300=0 total=1\n":                       proto.PressureWarn,
		"some avg10=45.00 avg60=1 avg300=0 total=1\n":                       proto.PressureCritical,
		"some avg10=x\n": "",
		"":               "",
	} {
		if got := parsePSI([]byte(in)); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	for used, want := range map[uint64]string{50: proto.PressureNormal, 92: proto.PressureWarn, 97: proto.PressureCritical} {
		if got := pressureByAvailable(&proto.SystemMemory{Total: 100, Used: used}); got != want {
			t.Errorf("used %d = %q", used, got)
		}
	}
	if pressureByAvailable(nil) != "" || pressureByAvailable(&proto.SystemMemory{}) != "" {
		t.Error("no memory: a pressure")
	}
	for n, want := range map[uint32]string{1: proto.PressureNormal, 2: proto.PressureWarn, 4: proto.PressureCritical, 0: "", 3: ""} {
		if got := pressureLevel(n); got != want {
			t.Errorf("level %d = %q", n, got)
		}
	}
}

func TestParseLoadAndUptime(t *testing.T) {
	if l := parseLoadavg([]byte("0.52 1.10 2.00 1/234 5678\n")); len(l) != 3 || l[2] != 2 {
		t.Errorf("load = %v", l)
	}
	for _, bad := range []string{"", "1 2", "1 x 3"} {
		if l := parseLoadavg([]byte(bad)); l != nil {
			t.Errorf("%q = %v", bad, l)
		}
	}
	if u := parseUptime([]byte("3600.75 7000.10\n")); u != 3600 {
		t.Errorf("uptime = %d", u)
	}
	for _, bad := range []string{"", "x", "-5 1"} {
		if u := parseUptime([]byte(bad)); u != 0 {
			t.Errorf("%q = %d", bad, u)
		}
	}
}

func TestParsePidStat(t *testing.T) {
	// A name holding a space and a parenthesis: fields count from the last ")".
	in := "4242 (my (odd) prog) S 1 4242 4242 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 7 0 12345 1000000 512 18446744073709551615"
	st, ok := parsePidStat([]byte(in))
	if !ok {
		t.Fatal("not read")
	}
	if st.name != "my (odd) prog" || st.ppid != 1 || st.utime != 250 || st.stime != 50 || st.threads != 7 || st.start != 12345 || st.rssPages != 512 {
		t.Errorf("stat = %+v", st)
	}
	for _, bad := range []string{"", "1 noparens S 1", "1 (x) S 1 2 3", "1 (x) S y 1 1 0 -1 0 0 0 0 0 1 1 0 0 20 0 1 0 1 1 1"} {
		if _, ok := parsePidStat([]byte(bad)); ok {
			t.Errorf("%q read", bad)
		}
	}
}

func TestParseUidAndCmdline(t *testing.T) {
	if uid, ok := parseUid([]byte("Name:\tbash\nUid:\t1000\t1000\t1000\t1000\n")); !ok || uid != 1000 {
		t.Errorf("uid = %d %v", uid, ok)
	}
	for _, bad := range []string{"", "Uid:\n", "Uid:\tx\n"} {
		if _, ok := parseUid([]byte(bad)); ok {
			t.Errorf("%q read", bad)
		}
	}
	if c := parseCmdline([]byte("go\x00test\x00-race\x00./...\x00")); c != "go test -race ./..." {
		t.Errorf("cmdline = %q", c)
	}
	if c := parseCmdline(nil); c != "" {
		t.Errorf("empty = %q", c)
	}
}

const vmStat = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               10000.
Pages active:                            300000.
Pages inactive:                          290000.
Pages speculative:                         5000.
Pages throttled:                              0.
Pages wired down:                        200000.
Pages purgeable:                          10000.
"Translation faults":                 999999999.
Pages copy-on-write:                   12345678.
File-backed pages:                       250000.
Anonymous pages:                         350000.
Pages stored in compressor:              500000.
Pages occupied by compressor:            150000.
Swapins:                                      0.
`

func TestParseVMStat(t *testing.T) {
	const page = 16384
	total := uint64(36) << 30
	m, ok := parseVMStat([]byte(vmStat), total)
	if !ok {
		t.Fatal("not read")
	}
	if m.Apps != 340000*page || m.Wired != 200000*page || m.Compressed != 150000*page || m.Cached != 260000*page {
		t.Errorf("apps %d wired %d compressed %d cached %d", m.Apps/page, m.Wired/page, m.Compressed/page, m.Cached/page)
	}
	if m.Used != m.Apps+m.Wired+m.Compressed || m.Used+m.Cached+m.Free != total {
		t.Errorf("used %d + cached %d + free %d != total %d", m.Used, m.Cached, m.Free, total)
	}
	// The total smaller than what is counted: clamped, never wrapped.
	m, ok = parseVMStat([]byte(vmStat), 1<<30)
	if !ok || m.Used != 1<<30 || m.Free != 0 || m.Cached != 0 {
		t.Errorf("small total: %+v", m)
	}
	// No page size line: 4096.
	m, ok = parseVMStat([]byte("Anonymous pages: 10.\nPages wired down: 5.\n"), 1<<30)
	if !ok || m.Apps != 10*4096 {
		t.Errorf("default page: %+v", m)
	}
	for _, bad := range []string{"", "Pages free: 10.\n"} {
		if _, ok := parseVMStat([]byte(bad), total); ok {
			t.Errorf("%q read", bad)
		}
	}
	if _, ok := parseVMStat([]byte(vmStat), 0); ok {
		t.Error("no total: read")
	}
}

func TestParseSysctlStructs(t *testing.T) {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[0:], 2048)
	binary.LittleEndian.PutUint32(b[4:], 1024)
	binary.LittleEndian.PutUint32(b[8:], 512)
	binary.LittleEndian.PutUint64(b[16:], 2048)
	if l := parseLoadavgSysctl(b); len(l) != 3 || l[0] != 1 || l[1] != 0.5 || l[2] != 0.25 {
		t.Errorf("load = %v", l)
	}
	if l := parseLoadavgSysctl(b[:20]); l != nil {
		t.Errorf("short = %v", l)
	}
	if l := parseLoadavgSysctl(make([]byte, 24)); l != nil {
		t.Errorf("zero scale = %v", l)
	}
	s := make([]byte, 32)
	binary.LittleEndian.PutUint64(s[0:], 4<<30)
	binary.LittleEndian.PutUint64(s[8:], 3<<30)
	binary.LittleEndian.PutUint64(s[16:], 1<<30)
	if tot, used, ok := parseSwapSysctl(s); !ok || tot != 4<<30 || used != 1<<30 {
		t.Errorf("swap = %d %d %v", tot, used, ok)
	}
	if _, _, ok := parseSwapSysctl(s[:10]); ok {
		t.Error("short swap read")
	}
}

func TestParsePS(t *testing.T) {
	in := "    1     0  12000   1:02.50     6-04:00:00 root  /sbin/launchd\n" +
		" 4410  4398 1443840   12:00.10      01:00:00 amit  /Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Helper (Renderer)\n" +
		"  77     1   100   0:00.00   00:05 _windowserver WindowServer\n" +
		"bad line\n" +
		"  9 1 x 0:00 00:01 root y\n" +
		"  10 1 1 0:00 nope root y\n"
	got := parsePS([]byte(in))
	if len(got) != 3 {
		t.Fatalf("got %d lines: %+v", len(got), got)
	}
	if got[0].elapsed != (6*24+4)*time.Hour || got[0].cpu != 62.5 || got[0].rss != 12000*1024 {
		t.Errorf("launchd = %+v", got[0])
	}
	if baseName(got[1].comm) != "Google Chrome Helper (Renderer)" || got[1].user != "amit" {
		t.Errorf("chrome = %q %q", got[1].comm, got[1].user)
	}
	if got[2].elapsed != 5*time.Second {
		t.Errorf("elapsed = %v", got[2].elapsed)
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]float64{
		"0:00": 0, "1:02.50": 62.5, "01:00:00": 3600, "2-00:00:01": 172801, "10:00": 600,
	} {
		if got, ok := parseClock(in); !ok || got != want {
			t.Errorf("%q = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "5", "1:2:3:4", "x:00", "1:-1", "-1-00:00", "a-00:00"} {
		if _, ok := parseClock(bad); ok {
			t.Errorf("%q read", bad)
		}
	}
}

func TestParseArgsAndNames(t *testing.T) {
	got := parseArgs([]byte("  52310 go test -race ./...\n    1 /sbin/launchd\nnot a pid\n  7\n"))
	if got[52310] != "go test -race ./..." || got[1] != "/sbin/launchd" || len(got) != 2 {
		t.Errorf("args = %v", got)
	}
	for in, want := range map[string]string{"/usr/bin/go": "go", "go": "go", "/x/": "/x/", "": ""} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q", in, got)
		}
	}
	if n := osRelease([]byte("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n")); n != "Ubuntu 24.04.1 LTS" {
		t.Errorf("os-release = %q", n)
	}
	if n := osRelease(nil); n != "" {
		t.Errorf("empty os-release = %q", n)
	}
	if m := cpuModel([]byte("processor\t: 0\nmodel name\t: Intel(R)  Xeon(R) CPU\n")); m != "Intel(R) Xeon(R) CPU" {
		t.Errorf("model = %q", m)
	}
	if m := cpuModel([]byte("processor : 0\nBogoMIPS : 50\n")); m != "" {
		t.Errorf("arm model = %q", m)
	}
}

func TestMachTicks(t *testing.T) {
	got := machTicks([]uint32{10, 5, 80, 2, 20, 10, 60, 0, 99})
	if len(got.Cores) != 2 {
		t.Fatalf("cores = %d", len(got.Cores))
	}
	if got.Cores[0].User != 12 || got.Cores[0].Busy != 17 || got.All.Busy != 47 || got.All.Idle != 140 {
		t.Errorf("ticks = %+v", got)
	}
	if got := machTicks(nil); len(got.Cores) != 0 || got.All.Busy != 0 {
		t.Errorf("empty = %+v", got)
	}
}
