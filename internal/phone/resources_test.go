package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/procs"
	"github.com/Amitgb14/conch/internal/proto"
)

// psSample is a machineSample from ps's own words, taken at at.
func psSample(at time.Time, ps string) *machineSample {
	return &machineSample{procs: procs.Sample{At: at, Procs: procs.Parse([]byte(ps))},
		mem: procs.Memory{Total: 16 << 30, Used: 10 << 30}}
}

func f64(v float64) *float64 { return &v }

// The server (100) runs two panes: a shell running claude, which runs
// node and git, and a shell alone. A third pane has exited and a fourth
// has no pid. 900 is not conch's.
const (
	psBefore = `  100     1  20480 0:01.00 /usr/local/bin/conch
  200   100   4096 0:00.10 -zsh
  201   200 307200 0:10.00 claude
  202   201 102400 0:02.00 node
  300   100   2048 0:00.00 /bin/zsh
  900     1 999999 1:00.00 Safari
`
	psAfter = `  100     1  20480 0:01.20 /usr/local/bin/conch
  200   100   4096 0:00.10 -zsh
  201   200 307200 0:11.00 claude
  202   201 102400 0:04.00 node
  203   201   1024 0:00.50 git
  300   100   2048 0:00.00 /bin/zsh
  900     1 999999 1:02.00 Safari
`
)

var resourcePanes = []proto.PaneInfo{
	{ID: "p1", PID: 200}, {ID: "p2", PID: 300}, {ID: "p3", PID: 777}, {ID: "p4", PID: 0},
}

func TestBuildResources(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	prev, cur := psSample(t0, psBefore), psSample(t0.Add(2*time.Second), psAfter)
	got := buildResources("local", prev, cur, 100, resourcePanes, 8)

	want := Resources{
		Machine: "local", SampledAt: t0.Add(2 * time.Second), Cores: 8,
		// Every process's 0.2+1+2+0.5+2 s over 2 s of 8 cores.
		CPUPercent: f64(35.6),
		MemTotal:   16 << 30, MemUsed: 10 << 30,
		Conch: ResourceUse{CPUCores: f64(1.85), Mem: (20480 + 4096 + 307200 + 102400 + 1024 + 2048) * 1024},
		Panes: []PaneResources{
			// node 2 s, claude 1 s, git 0.5 s over 2 s; the shell left out.
			{Pane: "local:p1", CPUCores: f64(1.75), Mem: (4096 + 307200 + 102400 + 1024) * 1024, Procs: 4,
				Top: []string{"node", "claude", "git"}},
			// A shell alone is what runs there.
			{Pane: "local:p2", CPUCores: f64(0), Mem: 2048 * 1024, Procs: 1, Top: []string{"zsh"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Fatalf("resources\n got %s\nwant %s", gj, wj)
	}
	// Zero CPU is said, not left out.
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"pane":"local:p2","cpu_cores":0,`) || strings.Contains(string(b), "null") {
		t.Errorf("json %s", b)
	}

	// One sample: memory, no CPU anywhere — the fields are left out.
	got = buildResources("local", nil, cur, 100, resourcePanes, 8)
	if got.CPUPercent != nil || got.Conch.CPUCores != nil || got.Panes[0].CPUCores != nil {
		t.Errorf("cpu from one sample: %+v", got)
	}
	// Before a rate, the biggest by memory.
	if !reflect.DeepEqual(got.Panes[0].Top, []string{"claude", "node", "git"}) {
		t.Errorf("top by memory %v", got.Panes[0].Top)
	}
	b, _ = json.Marshal(got)
	if strings.Contains(string(b), "cpu") || strings.Contains(string(b), "null") {
		t.Errorf("one sample json %s", b)
	}

	// Linux counts the machine's CPU itself, and that is believed over the
	// processes.
	prev.ticks, prev.ticksOK = procs.Ticks{Busy: 100, Total: 1000}, true
	cur.ticks, cur.ticksOK = procs.Ticks{Busy: 433, Total: 2000}, true
	if got := buildResources("local", prev, cur, 100, nil, 8); got.CPUPercent == nil || *got.CPUPercent != 33.3 {
		t.Errorf("from /proc/stat: %v", got.CPUPercent)
	}
	cur.ticks = prev.ticks // counters that did not move: unknown
	if got := buildResources("local", prev, cur, 100, nil, 8); got.CPUPercent != nil {
		t.Errorf("unmoved ticks: %v", *got.CPUPercent)
	}
}

func TestBuildResourcesEdges(t *testing.T) {
	t0 := time.Unix(1000, 0)
	cur := psSample(t0, psAfter)
	// No panes, no server running here: nothing of conch's, and lists that
	// are empty rather than null.
	got := buildResources("local", nil, cur, 4242, nil, 4)
	if got.Conch.Mem != 0 || got.Panes == nil || len(got.Panes) != 0 {
		t.Errorf("nothing: %+v", got)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"panes":[]`) {
		t.Errorf("json %s", b)
	}
	// Without the server's pid the panes are still conch's.
	got = buildResources("local", nil, cur, 0, resourcePanes, 4)
	if got.Conch.Mem != (4096+307200+102400+1024+2048)*1024 || len(got.Panes) != 2 {
		t.Errorf("no server pid: %+v", got)
	}
	// The same pid named by two panes is counted once, for the first.
	got = buildResources("local", nil, cur, 100, []proto.PaneInfo{{ID: "p1", PID: 300}, {ID: "p2", PID: 300}}, 4)
	if len(got.Panes) != 1 || got.Panes[0].Pane != "local:p1" {
		t.Errorf("twin panes: %+v", got.Panes)
	}
	// The clock going back gives no rate, and a sample without memory
	// reports none.
	prev := psSample(t0.Add(time.Second), psBefore)
	cur.mem = procs.Memory{}
	got = buildResources("local", prev, cur, 100, resourcePanes, 4)
	if got.CPUPercent != nil || got.Conch.CPUCores != nil || got.MemTotal != 0 {
		t.Errorf("clock back: %+v", got)
	}
	// A machine that was busier than it has cores (processes that started
	// between the samples) is still at most 100%.
	prev = psSample(t0.Add(-time.Second), "1 0 1 0:00.00 init\n")
	if got := buildResources("local", prev, cur, 100, nil, 1); got.CPUPercent == nil || *got.CPUPercent != 100 {
		t.Errorf("over 100: %v", got.CPUPercent)
	}
}

func TestTopProcs(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := &procs.Sample{At: t0, Procs: procs.Parse([]byte("1 0 10 0:00.00 bash\n2 1 10 0:00.00 node\n"))}
	cur := &procs.Sample{At: t0.Add(time.Second), Procs: procs.Parse([]byte(
		"1 0 10 0:05.00 bash\n2 1 10 0:01.00 node\n3 1 50 0:00.50 node\n4 1 900 0:00.00 /opt/My App/bin/My App\n" +
			"5 1 20 0:00.00 sh\n6 1 1 0:00.00 \n"))}
	// The root shell is left out however busy; two nodes are one name; a
	// shell under the pane's program is kept; an empty name is skipped.
	got := topProcs(prev, cur, []int{1, 2, 3, 4, 5, 6}, true)
	if !reflect.DeepEqual(got, []string{"node", "My App", "sh"}) {
		t.Errorf("top %v", got)
	}
	// A pane's program that is not a shell is what runs there.
	if got := topProcs(prev, cur, []int{2, 4}, true); !reflect.DeepEqual(got, []string{"node", "My App"}) {
		t.Errorf("agent root %v", got)
	}
	if got := topProcs(prev, cur, nil, true); got == nil || len(got) != 0 {
		t.Errorf("nothing %#v", got)
	}
}

// fakeMeter replaces the meter's sampling: each take is the next of
// samples, stamped with the meter's clock, and waits move the clock.
type fakeMeter struct {
	mu    sync.Mutex
	clock time.Time
	takes int
	waits []time.Duration
	fail  bool
}

func (fm *fakeMeter) install(m *resourceMeter) {
	m.now = func() time.Time { fm.mu.Lock(); defer fm.mu.Unlock(); return fm.clock }
	m.take = func() machineSample {
		fm.mu.Lock()
		defer fm.mu.Unlock()
		fm.takes++
		if fm.fail {
			return machineSample{procs: procs.Sample{At: fm.clock, Err: errors.New("ps: killed")}}
		}
		// Process 1 uses a tenth of a second for each take.
		ps := fmt.Sprintf("1 0 100 0:%02d.%02d init\n", fm.takes/10, fm.takes%10*10)
		return *psSample(fm.clock, ps)
	}
	m.wait = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fm.mu.Lock()
		defer fm.mu.Unlock()
		fm.waits = append(fm.waits, d)
		fm.clock = fm.clock.Add(d)
		return nil
	}
}

func (fm *fakeMeter) advance(d time.Duration) {
	fm.mu.Lock()
	fm.clock = fm.clock.Add(d)
	fm.mu.Unlock()
}

func TestResourceMeterCache(t *testing.T) {
	m := newResourceMeter()
	fm := &fakeMeter{clock: time.Unix(1000, 0)}
	fm.install(m)
	ctx := context.Background()
	gap := func(prev, cur *machineSample) time.Duration { return cur.procs.At.Sub(prev.procs.At) }

	// Cold: two samples half a second apart.
	prev, cur, err := m.samples(ctx)
	if err != nil || fm.takes != 2 || !reflect.DeepEqual(fm.waits, []time.Duration{resColdGap}) || gap(prev, cur) != resColdGap {
		t.Fatalf("cold: %v, %d takes, waits %v", err, fm.takes, fm.waits)
	}
	// A phone polling every two seconds: one sample each, against the last.
	last := cur
	for range 3 {
		fm.advance(2 * time.Second)
		prev, cur, err = m.samples(ctx)
		if err != nil || prev != last || gap(prev, cur) != 2*time.Second {
			t.Fatalf("poll: %v, prev %p (last %p), gap %v", err, prev, last, gap(prev, cur))
		}
		last = cur
	}
	if fm.takes != 5 || len(fm.waits) != 1 {
		t.Errorf("polling took %d samples, waited %v", fm.takes, fm.waits)
	}
	// Asked again within a second: the same pair, nothing sampled.
	fm.advance(resReuse - time.Millisecond)
	if p, c, _ := m.samples(ctx); p != prev || c != cur || fm.takes != 5 {
		t.Errorf("reuse: %d takes", fm.takes)
	}
	// The CPU between the samples is real: a tenth of a second a take.
	fm.advance(time.Second + time.Millisecond)
	prev, cur, _ = m.samples(ctx)
	if r := buildResources("local", prev, cur, 0, nil, 1); r.CPUPercent == nil || *r.CPUPercent != 5 || gap(prev, cur) != 2*time.Second {
		t.Errorf("rate over %v: %v", gap(prev, cur), r.CPUPercent)
	}
	// Exactly stale is still warm; past it, cold again.
	fm.advance(resStale)
	if _, _, err := m.samples(ctx); err != nil || fm.takes != 7 || len(fm.waits) != 1 {
		t.Errorf("at the limit: %v, %d takes, %d waits", err, fm.takes, len(fm.waits))
	}
	fm.advance(resStale + time.Millisecond)
	if _, _, err := m.samples(ctx); err != nil || fm.takes != 9 || len(fm.waits) != 2 {
		t.Errorf("stale: %v, %d takes, %d waits", err, fm.takes, len(fm.waits))
	}
	// A clock that went back is not trusted: cold.
	fm.advance(-time.Minute)
	if _, _, err := m.samples(ctx); err != nil || fm.takes != 11 || len(fm.waits) != 3 {
		t.Errorf("clock back: %v, %d takes, %d waits", err, fm.takes, len(fm.waits))
	}

	// ps failing: an error, and the next request starts cold.
	fm.advance(2 * time.Second)
	fm.fail = true
	if _, _, err := m.samples(ctx); err == nil || !strings.Contains(err.Error(), "killed") || m.cur != nil {
		t.Errorf("failed ps: %v, kept %v", err, m.cur)
	}
	if _, _, err := m.samples(ctx); err == nil || len(fm.waits) != 3 {
		t.Errorf("failed cold ps: %v, waits %v", err, fm.waits)
	}
	fm.fail = false
	fm.advance(2 * time.Second)
	if _, _, err := m.samples(ctx); err != nil || len(fm.waits) != 4 {
		t.Errorf("after a failure: %v, waits %v", err, fm.waits)
	}

	// A request that goes away during the cold wait gets nothing, and
	// nothing half-made is kept.
	fm.advance(time.Hour)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := m.samples(cctx); !errors.Is(err, context.Canceled) || m.cur != nil {
		t.Errorf("cancelled: %v, kept %v", err, m.cur)
	}
}

// Two phones at once: one samples, the other waits for it and reuses
// what it took.
func TestResourceMeterConcurrent(t *testing.T) {
	m := newResourceMeter()
	fm := &fakeMeter{clock: time.Unix(1000, 0)}
	fm.install(m)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := m.samples(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fm.takes != 2 {
		t.Errorf("8 requests took %d samples", fm.takes)
	}
}

func TestResourcesEndpoint(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	var pane proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 30"}, Cwd: f.dir, Cols: 80, Rows: 24}, &pane)
	if pane.PID <= 0 {
		t.Fatalf("pane %+v", pane)
	}
	// The test's own server runs in this process, so ps is told so.
	fm := &fakeMeter{clock: time.Now()}
	fm.install(f.g.meter)
	me := os.Getpid()
	f.g.meter.take = func() machineSample {
		fm.mu.Lock()
		defer fm.mu.Unlock()
		fm.takes++
		return *psSample(fm.clock, fmt.Sprintf("%d 1 51200 0:%02d.00 conch.test\n%d %d 1024 0:00.00 sleep\n%d 1 9 0:00.00 other\n",
			me, fm.takes, pane.PID, me, me+1))
	}

	for _, path := range []string{"/api/resources", "/api/resources?machine=local"} {
		var got Resources
		if status := p.get(path, &got); status != 200 {
			t.Fatalf("%s: %d", path, status)
		}
		if got.Machine != LocalMachine || got.Cores != runtime.NumCPU() || got.SampledAt.IsZero() || got.MemTotal != 16<<30 {
			t.Errorf("%s: %+v", path, got)
		}
		want := []PaneResources{{Pane: "local:" + pane.ID, CPUCores: f64(0), Mem: 1024 * 1024, Procs: 1, Top: []string{"sleep"}}}
		if !reflect.DeepEqual(got.Panes, want) {
			t.Errorf("%s panes %+v", path, got.Panes)
		}
		if got.Conch.Mem != (51200+1024)*1024 || got.Conch.CPUCores == nil || got.CPUPercent == nil {
			t.Errorf("%s conch %+v, cpu %v", path, got.Conch, got.CPUPercent)
		}
		_, raw := p.do("GET", path, nil)
		if strings.Contains(string(raw), "null") {
			t.Errorf("null in %s", raw)
		}
		fm.advance(2 * time.Second)
	}
	// The first was cold, the second a single sample after it.
	if fm.takes != 3 {
		t.Errorf("%d samples for two requests", fm.takes)
	}

	if status, b := p.do("GET", "/api/resources?machine=Bad!", nil); status != 400 || !strings.Contains(string(b), CodeBadRequest) {
		t.Errorf("a malformed machine: %d %s", status, b)
	}
	// ps failing is the computer being unable to say, not a crash.
	f.g.meter.take = func() machineSample {
		return machineSample{procs: procs.Sample{At: time.Now(), Err: errors.New("exec: \"ps\": not found")}}
	}
	fm.advance(time.Hour)
	status, b := p.do("GET", "/api/resources", nil)
	if status != 503 || !strings.Contains(string(b), CodeServerUnavailable) || !strings.Contains(string(b), "not found") {
		t.Errorf("ps failing: %d %s", status, b)
	}
}

// TestResourcesHonoursMachines: another machine is out of reach for a
// device not given it, not found when it is not there, and, given,
// cannot say yet what it is using.
func TestResourcesHonoursMachines(t *testing.T) {
	f := newFixture(t)
	addMachine(t, f, "busybox", true)
	p := f.pair(PermView)
	taken := false
	f.g.meter.take = func() machineSample { taken = true; return machineSample{} }

	if status, b := p.do("GET", "/api/resources?machine=busybox", nil); status != 403 || !strings.Contains(string(b), CodeForbidden) {
		t.Fatalf("an ungranted machine: %d %s", status, b)
	}
	f.grant(p, "busybox", "nowhere")
	waitMachine(t, f, p, "busybox", MachineOnline)
	status, b := p.do("GET", "/api/resources?machine=busybox", nil)
	var e struct{ Error APIError }
	json.Unmarshal(b, &e)
	if status != 503 || e.Error.Code != CodeServerUnavailable || e.Error.Message != "resource usage is only available for this computer for now" {
		t.Fatalf("a granted remote machine: %d %s", status, b)
	}
	if status, b := p.do("GET", "/api/resources?machine=nowhere", nil); status != 404 || !strings.Contains(string(b), CodeNotFound) {
		t.Fatalf("a machine that is not there: %d %s", status, b)
	}
	if taken {
		t.Error("this computer was sampled for another machine")
	}
}

// The real thing, read-only: ps, and the system's memory and CPU, parse
// where conch runs.
func TestTakeMachineSampleReal(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("conch measures Linux and macOS")
	}
	s := takeMachineSample()
	if s.procs.Err != nil {
		t.Fatal(s.procs.Err)
	}
	if _, ok := s.procs.Procs[os.Getpid()]; !ok {
		t.Errorf("this process is not among %d", len(s.procs.Procs))
	}
	if s.mem.Total <= 0 || s.mem.Used <= 0 || s.mem.Used > s.mem.Total {
		t.Errorf("memory %+v", s.mem)
	}
	if s.ticksOK != (runtime.GOOS == "linux") {
		t.Errorf("ticks %+v %v", s.ticks, s.ticksOK)
	}
}
