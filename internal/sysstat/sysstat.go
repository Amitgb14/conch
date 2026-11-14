// Package sysstat reads the machine conch's server runs on: its CPU and
// each core, memory and where it goes, load, disk, and every process with
// its CPU and memory — for system.stats, the window beside the resource
// monitor. It reads only when asked, and keeps an hour of history while
// somebody keeps asking.
package sysstat

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Proc is one process at one moment. CPU is the processor time it has
// used so far, in seconds: only the difference between two readings says
// how busy it is.
type Proc struct {
	PID, PPID int
	Name      string
	User      string
	RSS       uint64 // bytes
	CPU       float64
	Threads   int
	Started   time.Time
}

// Source is where the readings come from: the machine's own (Local), or a
// test's. A reader left nil is something the machine does not say.
type Source struct {
	Host     func() proto.SystemHost
	Ticks    func() (CPUTicks, bool)
	Memory   func(ctx context.Context) (*proto.SystemMemory, bool)
	Load     func() []float64
	Disk     func() *proto.SystemDisk
	Procs    func(ctx context.Context) ([]Proc, error)
	Commands func(ctx context.Context, pids []int) map[int]string
}

const (
	// historyKeep is how far back the history goes.
	historyKeep = time.Hour
	// historyEvery is how often a point is added while nobody asks but
	// somebody did lately, and the least spacing between two points.
	historyEvery = 5 * time.Second
	// pointGap is the least time between two points: a window asking
	// every two seconds adds each, two asks at once add one.
	pointGap = 1500 * time.Millisecond
	// keepFor is how long after the last question the history is kept up.
	keepFor = 10 * time.Minute
	// fresh is how old a reading may be and still be the answer: two
	// windows asking at once are not each worth a ps.
	fresh = time.Second
	// listed is how many of the busiest, and of the largest, are listed.
	listed = 30
	// listMax caps the list with conch's own processes added.
	listMax = 150
)

// Sampler answers system.stats. Its zero value is not usable: New.
type Sampler struct {
	src  Source
	now  func() time.Time
	tick time.Duration // historyEvery, shorter in tests

	mu        sync.Mutex
	host      *proto.SystemHost
	boot      int64 // when the machine started, in Unix seconds
	prevTicks *CPUTicks
	prevProcs map[int]Proc
	procsAt   time.Time // when prevProcs was read: a summary reads none
	last      *reading  // the newest full reading, for fresh
	hist      []proto.SystemPoint
	asked     time.Time
	keeping   bool
	stop      chan struct{}
	stopped   bool
}

// reading is one look at the machine.
type reading struct {
	at    time.Time
	cpu   *proto.SystemCPU
	mem   *proto.SystemMemory
	procs []proto.SystemProcess // every process, CPU set after the first
}

// New makes a sampler over src.
func New(src Source) *Sampler {
	return &Sampler{src: src, now: time.Now, tick: historyEvery, stop: make(chan struct{})}
}

// Stop ends the history's upkeep; Stats still answers afterwards.
func (s *Sampler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stop)
	}
}

// Stats is the machine now. tag names the processes conch started — it is
// given every process, and its parents, before any is picked to list.
func (s *Sampler) Stats(ctx context.Context, p proto.SystemStatsParams, tag func([]proto.SystemProcess)) proto.SystemStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.asked = now
	s.keepUp()
	host := s.hostNow(now)
	out := proto.SystemStats{At: now, Host: host}
	if s.src.Load != nil {
		out.Load = s.src.Load()
	}
	if s.src.Disk != nil {
		out.Disk = s.src.Disk()
	}

	r := s.last
	if r == nil || now.Sub(r.at) >= fresh || (!p.Summary && r.procs == nil) {
		r = s.read(ctx, now, !p.Summary)
	}
	out.CPU, out.Memory = r.cpu, r.mem
	if r.procs != nil {
		out.Procs = len(r.procs)
	}
	if p.Summary {
		return out
	}
	all := slices.Clone(r.procs)
	if tag != nil {
		tag(all)
	}
	out.Processes = Pick(all, listed, listMax)
	if s.src.Commands != nil && len(out.Processes) > 0 {
		pids := make([]int, len(out.Processes))
		for i, pr := range out.Processes {
			pids[i] = pr.PID
		}
		cmds := s.src.Commands(ctx, pids)
		for i := range out.Processes {
			out.Processes[i].Command = cmds[out.Processes[i].PID]
		}
	}
	for _, pt := range s.hist {
		if pt.At.After(p.Since) {
			out.History = append(out.History, pt)
		}
	}
	return out
}

// hostNow is the host, read once — what a machine is does not change
// while the server runs — with its uptime brought up to now.
func (s *Sampler) hostNow(now time.Time) proto.SystemHost {
	if s.host == nil {
		h := proto.SystemHost{}
		if s.src.Host != nil {
			h = s.src.Host()
		}
		if h.Uptime > 0 {
			s.boot = now.Unix() - h.Uptime
		}
		s.host = &h
	}
	h := *s.host
	if s.boot > 0 {
		h.Uptime = now.Unix() - s.boot
	}
	return h
}

// read looks at the machine, every process too when procs is set or when
// the processes are the only way to the CPU, and adds the history point.
// Called with s.mu held.
func (s *Sampler) read(ctx context.Context, now time.Time, procs bool) *reading {
	r := &reading{at: now}
	var ticks CPUTicks
	haveTicks := false
	if s.src.Ticks != nil {
		ticks, haveTicks = s.src.Ticks()
	}
	if s.src.Memory != nil {
		r.mem, _ = s.src.Memory(ctx)
	}
	if !haveTicks {
		procs = true // the processes' own time is all there is to go by
	}
	// Each against its own last reading: a summary in between reads the
	// ticks and not the processes.
	dt := now.Sub(s.procsAt).Seconds()
	if procs && s.src.Procs != nil {
		if list, err := s.src.Procs(ctx); err == nil {
			cur := make(map[int]Proc, len(list))
			r.procs = make([]proto.SystemProcess, 0, len(list))
			used := 0.0
			for _, p := range list {
				cur[p.PID] = p
				sp := proto.SystemProcess{PID: p.PID, PPID: p.PPID, Name: p.Name, User: p.User, RSS: p.RSS, Threads: p.Threads, Started: p.Started}
				// The same pid and name, or it is another process since.
				if was, ok := s.prevProcs[p.PID]; ok && was.Name == p.Name && dt > 0 && s.prevProcs != nil {
					d := max(p.CPU-was.CPU, 0)
					used += d
					pct := d / dt * 100
					sp.CPU = &pct
				}
				r.procs = append(r.procs, sp)
			}
			if !haveTicks && s.prevProcs != nil && dt > 0 {
				cores := max(s.hostNow(now).Cores, 1)
				total := min(used/dt/float64(cores)*100, 100)
				r.cpu = &proto.SystemCPU{Total: total}
			}
			s.prevProcs = cur
			s.procsAt = now
		}
	}
	if haveTicks {
		if s.prevTicks != nil {
			if total, user, sys, ok := share(s.prevTicks.All, ticks.All); ok {
				r.cpu = &proto.SystemCPU{Total: total, User: user, System: sys, Split: user+sys > 0}
				if len(ticks.Cores) == len(s.prevTicks.Cores) {
					for i := range ticks.Cores {
						c, _, _, _ := share(s.prevTicks.Cores[i], ticks.Cores[i])
						r.cpu.Cores = append(r.cpu.Cores, c)
					}
				}
			}
		}
		t := ticks
		s.prevTicks = &t
	}
	s.last = r
	s.addPoint(r)
	return r
}

// addPoint adds r to the history, unless the last point is too recent,
// and drops what is older than an hour.
func (s *Sampler) addPoint(r *reading) {
	// Without a CPU (the first reading) a point would draw as an idle machine.
	if r.cpu == nil {
		return
	}
	if n := len(s.hist); n > 0 && r.at.Sub(s.hist[n-1].At) < pointGap {
		return
	}
	pt := proto.SystemPoint{At: r.at}
	if r.cpu != nil {
		pt.CPU, pt.User, pt.System = r.cpu.Total, r.cpu.User, r.cpu.System
	}
	if r.mem != nil {
		pt.MemUsed, pt.Compressed = r.mem.Used, r.mem.Compressed
	}
	s.hist = append(s.hist, pt)
	cut := 0
	for cut < len(s.hist) && r.at.Sub(s.hist[cut].At) > historyKeep {
		cut++
	}
	if cut > 0 {
		s.hist = slices.Clone(s.hist[cut:])
	}
}

// keepUp starts the history's upkeep if it is not running: a point every
// few seconds until nobody has asked for a while. Called with s.mu held.
func (s *Sampler) keepUp() {
	if s.keeping || s.stopped {
		return
	}
	s.keeping = true
	go func() {
		t := time.NewTicker(s.tick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				s.mu.Lock()
				s.keeping = false
				s.mu.Unlock()
				return
			case <-t.C:
			}
			s.mu.Lock()
			now := s.now()
			if now.Sub(s.asked) > keepFor {
				s.keeping = false
				s.mu.Unlock()
				return
			}
			if n := len(s.hist); n == 0 || now.Sub(s.hist[n-1].At) >= historyEvery*9/10 {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				s.read(ctx, now, false)
				cancel()
			}
			s.mu.Unlock()
		}
	}()
}

// Pick is what a window lists of every process: the n busiest and the n
// largest, and every one conch started, at most max, the busiest first.
func Pick(all []proto.SystemProcess, n, most int) []proto.SystemProcess {
	cpu := func(p proto.SystemProcess) float64 {
		if p.CPU == nil {
			return -1
		}
		return *p.CPU
	}
	byCPU := slices.Clone(all)
	slices.SortStableFunc(byCPU, func(a, b proto.SystemProcess) int {
		switch {
		case cpu(a) > cpu(b):
			return -1
		case cpu(a) < cpu(b):
			return 1
		}
		return compareRSS(a, b)
	})
	keep := map[int]bool{}
	var out []proto.SystemProcess
	add := func(p proto.SystemProcess) {
		if !keep[p.PID] && len(out) < most {
			keep[p.PID] = true
			out = append(out, p)
		}
	}
	for i := 0; i < len(byCPU) && i < n; i++ {
		add(byCPU[i])
	}
	byRSS := slices.Clone(all)
	slices.SortStableFunc(byRSS, compareRSS)
	for i := 0; i < len(byRSS) && i < n; i++ {
		add(byRSS[i])
	}
	for _, p := range byCPU {
		if p.Pane != "" || p.Conch != "" {
			add(p)
		}
	}
	slices.SortStableFunc(out, func(a, b proto.SystemProcess) int {
		switch {
		case cpu(a) > cpu(b):
			return -1
		case cpu(a) < cpu(b):
			return 1
		}
		return compareRSS(a, b)
	})
	return out
}

func compareRSS(a, b proto.SystemProcess) int {
	switch {
	case a.RSS > b.RSS:
		return -1
	case a.RSS < b.RSS:
		return 1
	}
	return a.PID - b.PID
}

// Trees is each root's process and everything under it, a process
// counted under the first root that reaches it: so the panes are given
// before the server, whose children they are.
func Trees(all []proto.SystemProcess, roots []int) map[int][]int {
	children := map[int][]int{}
	present := map[int]bool{}
	for _, p := range all {
		present[p.PID] = true
		if p.PID != p.PPID {
			children[p.PPID] = append(children[p.PPID], p.PID)
		}
	}
	claimed := map[int]bool{}
	out := map[int][]int{}
	for _, root := range roots {
		if root <= 0 || !present[root] || claimed[root] {
			continue
		}
		stack := []int{root}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if claimed[pid] {
				continue
			}
			claimed[pid] = true
			out[root] = append(out[root], pid)
			stack = append(stack, children[pid]...)
		}
	}
	return out
}
