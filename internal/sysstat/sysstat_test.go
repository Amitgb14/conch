package sysstat

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// fake is a machine a test sets: its clock, ticks and processes move only
// when the test moves them.
type fake struct {
	mu      sync.Mutex
	now     time.Time
	ticks   *CPUTicks
	procs   []Proc
	psCalls int
	cmds    int
}

func (f *fake) source() Source {
	return Source{
		Host: func() proto.SystemHost { return proto.SystemHost{Name: "box", Cores: 2, Uptime: 100} },
		Ticks: func() (CPUTicks, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.ticks == nil {
				return CPUTicks{}, false
			}
			return *f.ticks, true
		},
		Memory: func(context.Context) (*proto.SystemMemory, bool) {
			return &proto.SystemMemory{Total: 100, Used: 40, Compressed: 5, Free: 60}, true
		},
		Load: func() []float64 { return []float64{1, 2, 3} },
		Procs: func(context.Context) ([]Proc, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.psCalls++
			return append([]Proc(nil), f.procs...), nil
		},
		Commands: func(_ context.Context, pids []int) map[int]string {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.cmds++
			out := map[int]string{}
			for _, p := range pids {
				out[p] = "cmd"
			}
			return out
		},
	}
}

func newFake(t *testing.T, f *fake) *Sampler {
	t.Helper()
	f.now = time.Unix(1_000_000, 0)
	s := New(f.source())
	s.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	t.Cleanup(s.Stop)
	return s
}

func (f *fake) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func TestStatsFirstThenSecond(t *testing.T) {
	f := &fake{ticks: &CPUTicks{All: Ticks{Busy: 100, Idle: 100, User: 60, Sys: 40}, Cores: []Ticks{{Busy: 50, Idle: 50}, {Busy: 50, Idle: 50}}},
		procs: []Proc{{PID: 10, PPID: 1, Name: "go", CPU: 1, RSS: 500}, {PID: 11, PPID: 1, Name: "node", CPU: 5, RSS: 900}}}
	s := newFake(t, f)
	first := s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if first.CPU != nil {
		t.Errorf("first reading has a CPU: %+v", first.CPU)
	}
	if first.Host.Name != "box" || first.Host.Uptime != 100 || len(first.Load) != 3 || first.Memory == nil || first.Procs != 2 {
		t.Errorf("first = %+v", first)
	}
	for _, p := range first.Processes {
		if p.CPU != nil {
			t.Errorf("pid %d has a CPU on the first reading", p.PID)
		}
		if p.Command != "cmd" {
			t.Errorf("pid %d command = %q", p.PID, p.Command)
		}
	}

	f.advance(2 * time.Second)
	f.mu.Lock()
	f.ticks = &CPUTicks{All: Ticks{Busy: 150, Idle: 150, User: 90, Sys: 60}, Cores: []Ticks{{Busy: 90, Idle: 60}, {Busy: 60, Idle: 90}}}
	f.procs = []Proc{{PID: 10, PPID: 1, Name: "go", CPU: 3, RSS: 500}, {PID: 11, PPID: 1, Name: "other", CPU: 9, RSS: 900}}
	f.mu.Unlock()
	second := s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if second.CPU == nil || !near(second.CPU.Total, 50) || !near(second.CPU.User, 30) || !second.CPU.Split {
		t.Fatalf("cpu = %+v", second.CPU)
	}
	if len(second.CPU.Cores) != 2 || !near(second.CPU.Cores[0], 80) || !near(second.CPU.Cores[1], 20) {
		t.Errorf("cores = %v", second.CPU.Cores)
	}
	if second.Host.Uptime != 102 {
		t.Errorf("uptime = %d, want 102", second.Host.Uptime)
	}
	byPID := map[int]proto.SystemProcess{}
	for _, p := range second.Processes {
		byPID[p.PID] = p
	}
	if c := byPID[10].CPU; c == nil || !near(*c, 100) {
		t.Errorf("go cpu = %v, want 100 (2 s over 2 s)", c)
	}
	if byPID[11].CPU != nil {
		t.Error("a pid now naming another program kept the old one's CPU")
	}
	if len(second.History) != 1 || !near(second.History[0].CPU, 50) || second.History[0].MemUsed != 40 {
		t.Errorf("history = %d points", len(second.History))
	}
}

func TestStatsWithoutTicksUsesProcesses(t *testing.T) {
	f := &fake{procs: []Proc{{PID: 1, Name: "a", CPU: 0}, {PID: 2, Name: "b", CPU: 0}}}
	s := newFake(t, f)
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	f.advance(2 * time.Second)
	f.mu.Lock()
	f.procs = []Proc{{PID: 1, Name: "a", CPU: 1}, {PID: 2, Name: "b", CPU: 1}}
	f.mu.Unlock()
	got := s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	// 2 s of CPU over 2 s on 2 cores: half the machine.
	if got.CPU == nil || !near(got.CPU.Total, 50) || got.CPU.Split || len(got.CPU.Cores) != 0 {
		t.Errorf("cpu = %+v", got.CPU)
	}
	if got.Processes != nil || got.History != nil {
		t.Error("a summary listed processes or history")
	}
}

func TestStatsFreshReadingIsReused(t *testing.T) {
	f := &fake{ticks: &CPUTicks{}, procs: []Proc{{PID: 1, Name: "a"}}}
	s := newFake(t, f)
	s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	f.advance(300 * time.Millisecond)
	s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if f.psCalls != 1 {
		t.Errorf("ps ran %d times for two asks within a second", f.psCalls)
	}
	// A summary needs no processes and a fresh full reading answers it.
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	if f.psCalls != 1 {
		t.Errorf("a summary ran ps: %d", f.psCalls)
	}
	f.advance(2 * time.Second)
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	if f.psCalls != 1 {
		t.Errorf("a summary with ticks ran ps: %d", f.psCalls)
	}
	// The summary's reading has no processes: a full ask right after reads again.
	s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if f.psCalls != 2 {
		t.Errorf("a full ask after a summary did not list processes: %d", f.psCalls)
	}
}

func TestStatsHistorySinceAndPruning(t *testing.T) {
	f := &fake{ticks: &CPUTicks{}}
	s := newFake(t, f)
	for i := 0; i < 10; i++ {
		f.mu.Lock()
		f.ticks = &CPUTicks{All: Ticks{Busy: uint64(i) * 100, Idle: uint64(i) * 100}}
		f.mu.Unlock()
		s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
		f.advance(10 * time.Minute)
	}
	f.mu.Lock()
	f.ticks = &CPUTicks{All: Ticks{Busy: 1000, Idle: 1000}}
	f.mu.Unlock()
	got := s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	for _, p := range got.History {
		if f.now.Sub(p.At) > time.Hour {
			t.Errorf("a point from %v is past the hour", f.now.Sub(p.At))
		}
	}
	if len(got.History) != 7 {
		t.Errorf("history = %d points, want 7 (the last hour, ends included)", len(got.History))
	}
	since := got.History[len(got.History)-2].At
	again := s.Stats(context.Background(), proto.SystemStatsParams{Since: since}, nil)
	if len(again.History) != 1 || !again.History[0].At.After(since) {
		t.Errorf("since: %d points", len(again.History))
	}
	// Two asks closer than the gap make one point.
	n := len(s.hist)
	f.advance(fresh)
	s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if len(s.hist) != n {
		t.Errorf("points %d → %d a second apart", n, len(s.hist))
	}
}

func TestStatsTagsBeforePicking(t *testing.T) {
	var procs []Proc
	for i := 1; i <= 100; i++ {
		procs = append(procs, Proc{PID: i, Name: "p", RSS: uint64(i)})
	}
	f := &fake{ticks: &CPUTicks{}, procs: procs}
	s := newFake(t, f)
	seen := 0
	got := s.Stats(context.Background(), proto.SystemStatsParams{}, func(all []proto.SystemProcess) {
		seen = len(all)
		for i := range all {
			if all[i].PID == 1 { // the smallest: listed only because it is conch's
				all[i].Pane = "pane-1"
			}
		}
	})
	if seen != 100 {
		t.Errorf("tag saw %d processes, want every one", seen)
	}
	found := false
	for _, p := range got.Processes {
		found = found || (p.PID == 1 && p.Pane == "pane-1")
	}
	if !found {
		t.Error("conch's own small process was not listed")
	}
	if got.Procs != 100 {
		t.Errorf("procs = %d", got.Procs)
	}
}

func TestKeepUpAddsPointsThenStops(t *testing.T) {
	// No ticks: the CPU comes from the (idle) processes, a reading every time.
	f := &fake{}
	s := newFake(t, f)
	s.tick = 5 * time.Millisecond
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	waitFor(t, func() bool {
		f.advance(historyEvery)
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.hist) >= 3
	})
	// Nobody asks for longer than keepFor: the upkeep ends by itself.
	f.advance(keepFor + time.Minute)
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.keeping })
	// Asked again, it starts again; Stop ends it.
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	s.mu.Lock()
	running := s.keeping
	s.mu.Unlock()
	if !running {
		t.Error("upkeep did not start again")
	}
	s.Stop()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.keeping })
	s.Stop() // twice is fine
	if got := s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil); got.Memory == nil {
		t.Error("stopped sampler does not answer")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStatsWithNothingToRead(t *testing.T) {
	s := New(Source{})
	t.Cleanup(s.Stop)
	got := s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if got.CPU != nil || got.Memory != nil || got.Processes != nil || got.History != nil || got.Procs != 0 {
		t.Errorf("empty source = %+v", got)
	}
}

func pct(v float64) *float64 { return &v }

func TestPick(t *testing.T) {
	all := []proto.SystemProcess{
		{PID: 1, CPU: pct(5), RSS: 10},
		{PID: 2, CPU: pct(300), RSS: 1},
		{PID: 3, RSS: 9000}, // no CPU yet, the largest
		{PID: 4, CPU: pct(1), RSS: 2, Conch: "server"},
		{PID: 5, CPU: pct(50), RSS: 5},
	}
	got := Pick(all, 1, 10)
	pids := []int{}
	for _, p := range got {
		pids = append(pids, p.PID)
	}
	// Busiest (2), largest (3), conch's (4); busiest first, no CPU last.
	if len(pids) != 3 || pids[0] != 2 || pids[1] != 4 || pids[2] != 3 {
		t.Errorf("picked %v", pids)
	}
	if got := Pick(all, 10, 2); len(got) != 2 {
		t.Errorf("max 2: %d", len(got))
	}
	if got := Pick(nil, 10, 10); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

func TestTrees(t *testing.T) {
	all := []proto.SystemProcess{
		{PID: 1, PPID: 0}, {PID: 100, PPID: 1}, // the server
		{PID: 200, PPID: 100}, {PID: 201, PPID: 200}, {PID: 202, PPID: 201}, // a pane
		{PID: 300, PPID: 100}, // another pane
		{PID: 150, PPID: 100}, // the server's own helper
		{PID: 7, PPID: 7},     // its own parent
	}
	got := Trees(all, []int{200, 300, 999, 100, 200})
	if len(got[200]) != 3 || len(got[300]) != 1 || len(got[999]) != 0 {
		t.Errorf("panes = %v", got)
	}
	if len(got[100]) != 2 { // itself and 150, the panes already claimed
		t.Errorf("server = %v", got[100])
	}
	if got := Trees(all, []int{7}); len(got[7]) != 1 {
		t.Errorf("self-parented = %v", got)
	}
}

// TestLocal reads this machine for real: nothing is changed by it, and
// what the parsers were tested on above is what this machine prints.
func TestLocal(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no reader for " + runtime.GOOS)
	}
	s := New(Local())
	t.Cleanup(s.Stop)
	ctx := context.Background()
	s.Stats(ctx, proto.SystemStatsParams{}, nil)
	time.Sleep(1100 * time.Millisecond) // past fresh, so it reads again
	got := s.Stats(ctx, proto.SystemStatsParams{}, nil)
	if got.Host.Cores < 1 || got.Host.OS == "" {
		t.Errorf("host = %+v", got.Host)
	}
	if got.Memory == nil || got.Memory.Total == 0 || got.Memory.Used > got.Memory.Total {
		t.Errorf("memory = %+v", got.Memory)
	}
	if got.CPU == nil || got.CPU.Total < 0 || got.CPU.Total > 100 {
		t.Errorf("cpu = %+v", got.CPU)
	}
	if len(got.Load) != 3 {
		t.Errorf("load = %v", got.Load)
	}
	if got.Disk == nil || got.Disk.Total == 0 || got.Disk.Free > got.Disk.Total {
		t.Errorf("disk = %+v", got.Disk)
	}
	if got.Procs < 2 || len(got.Processes) == 0 {
		t.Fatalf("procs = %d, listed %d", got.Procs, len(got.Processes))
	}
	// This test's own process is among the busiest or the largest of a
	// quiet machine only by luck; it is in the full list for certain.
	all, err := Local().Procs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := false
	for _, p := range all {
		me = me || (p.PID == os.Getpid() && p.RSS > 0)
	}
	if !me {
		t.Error("this process is not in the list")
	}
	withCmd := 0
	for _, p := range got.Processes {
		if p.Command != "" {
			withCmd++
		}
	}
	if withCmd == 0 {
		t.Error("no listed process has its command")
	}
}

// A summary or a history point between two full readings reads the
// ticks, not the processes: a process's CPU is still its time since the
// last reading *of processes*. Measured against the summary's moment it
// came out 52.8 cores for `yes` in a live window.
func TestProcessCPUIsOverTheProcessesOwnInterval(t *testing.T) {
	f := &fake{ticks: &CPUTicks{All: Ticks{Busy: 0, Idle: 0}}, procs: []Proc{{PID: 7, Name: "yes", CPU: 10}}}
	s := newFake(t, f)
	s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	f.advance(1500 * time.Millisecond)
	f.mu.Lock()
	f.ticks = &CPUTicks{All: Ticks{Busy: 50, Idle: 50}}
	f.mu.Unlock()
	s.Stats(context.Background(), proto.SystemStatsParams{Summary: true}, nil)
	f.advance(500 * time.Millisecond)
	f.mu.Lock()
	f.ticks = &CPUTicks{All: Ticks{Busy: 100, Idle: 100}}
	f.procs = []Proc{{PID: 7, Name: "yes", CPU: 12}} // 2 s of CPU in 2 s
	f.mu.Unlock()
	got := s.Stats(context.Background(), proto.SystemStatsParams{}, nil)
	if len(got.Processes) != 1 || got.Processes[0].CPU == nil || !near(*got.Processes[0].CPU, 100) {
		c := -1.0
		if len(got.Processes) == 1 && got.Processes[0].CPU != nil {
			c = *got.Processes[0].CPU
		}
		t.Errorf("yes = %v%%, want 100%% (one core)", c)
	}
}
