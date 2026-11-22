package phone

import (
	"cmp"
	"context"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/procs"
	"github.com/Amitgb14/conch/internal/proto"
)

// resources is GET /api/resources: the TUI's resource monitor for a phone.
// It is this computer's alone: the server protocol says nothing about what
// a machine is using, and the gateway does not reach into another machine
// over ssh to find out, so a remote machine is server_unavailable.
func (g *Gateway) resources(rq *request) (any, *APIError) {
	id := rq.r.URL.Query().Get("machine")
	local := id == "" || id == LocalMachine
	c, machine, aerr := g.machineFor(rq.dev, id)
	switch {
	case aerr != nil && (local || aerr.Code != CodeServerUnavailable):
		return nil, aerr
	case !local:
		// Malformed, out of reach and unknown machines were refused above,
		// as /api/project refuses them.
		return nil, apiErr(CodeServerUnavailable, "resource usage is only available for this computer for now")
	}
	var panes proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &panes); aerr != nil {
		return nil, aerr
	}
	prev, cur, err := g.meter.samples(rq.r.Context())
	if err != nil {
		return nil, apiErr(CodeServerUnavailable, "could not list this computer's processes: "+err.Error())
	}
	return buildResources(machine, prev, cur, c.Server.PID, panes.Panes, runtime.NumCPU()), nil
}

const (
	// resColdGap is how far apart a cold request's two samples are: long
	// enough for a CPU rate to mean something, short enough to wait for.
	resColdGap = 500 * time.Millisecond
	// resReuse: a sample younger than this answers again rather than
	// taking another, so two phones, or one asking twice, do not make a
	// rate out of a few milliseconds.
	resReuse = time.Second
	// resStale: an older sample is not used for a rate, which would be an
	// average over minutes nobody was looking at; the request is cold.
	resStale = 15 * time.Second
	// psTimeout bounds one ps.
	psTimeout = 5 * time.Second
)

// machineSample is one look at this computer.
type machineSample struct {
	procs   procs.Sample
	ticks   procs.Ticks
	ticksOK bool // the system counts CPU time as a whole (Linux)
	mem     procs.Memory
}

func takeMachineSample() machineSample {
	s := machineSample{procs: procs.Take(psTimeout)}
	s.ticks, s.ticksOK = procs.ReadTicks()
	// Memory unread is 0, which the app shows as unknown; it is not worth
	// failing the processes for.
	s.mem, _ = procs.ReadMemory()
	return s
}

// resourceMeter keeps this computer's last two samples, so a phone asking
// every couple of seconds gets CPU over the time since it last asked. It
// samples only when asked: nothing runs while nobody is looking.
type resourceMeter struct {
	mu        sync.Mutex
	prev, cur *machineSample

	// Tests replace these.
	take func() machineSample
	now  func() time.Time
	wait func(ctx context.Context, d time.Duration) error
}

func newResourceMeter() *resourceMeter {
	return &resourceMeter{take: takeMachineSample, now: time.Now, wait: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// samples is the pair a request is answered from. A cold request — the
// first, or the first after resStale — takes two samples resColdGap apart,
// so it is answered in full rather than with CPU missing until the next.
// The lock is held while sampling: a request arriving meanwhile then finds
// a fresh sample and reuses it.
func (m *resourceMeter) samples(ctx context.Context) (prev, cur *machineSample, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		switch age := m.now().Sub(m.cur.procs.At); {
		case age >= 0 && age < resReuse:
			return m.prev, m.cur, nil
		case age >= 0 && age <= resStale:
			return m.next(m.cur)
		}
	}
	m.prev, m.cur = nil, nil
	first := m.take()
	if first.procs.Err != nil {
		return nil, nil, first.procs.Err
	}
	if err := m.wait(ctx, resColdGap); err != nil {
		return nil, nil, err
	}
	return m.next(&first)
}

// next takes a sample after prev and keeps the two. A failed one forgets
// both, so the next request starts cold.
func (m *resourceMeter) next(prev *machineSample) (*machineSample, *machineSample, error) {
	s := m.take()
	if s.procs.Err != nil {
		m.prev, m.cur = nil, nil
		return nil, nil, s.procs.Err
	}
	m.prev, m.cur = prev, &s
	return m.prev, m.cur, nil
}

// buildResources adds up cur, with CPU over the time since prev, as the
// TUI's monitor does: each pane is its program and everything under it,
// and conch is the server with all of them. A pane whose program is not
// running (it exited, or its pid is not this computer's) is left out.
func buildResources(machine string, prev, cur *machineSample, serverPID int, panes []proto.PaneInfo, cores int) Resources {
	out := Resources{Machine: machine, SampledAt: cur.procs.At.UTC().Truncate(time.Second), Cores: cores,
		MemTotal: cur.mem.Total, MemUsed: cur.mem.Used, Panes: []PaneResources{}}
	ps := &cur.procs
	var before *procs.Sample
	if prev != nil {
		before = &prev.procs
	}
	elapsed, rate := procs.Elapsed(before, ps)
	cores64 := func(pids []int) *float64 {
		if !rate {
			return nil
		}
		return round(float64(procs.Used(before, ps, pids))/float64(elapsed), 1000)
	}

	children := procs.Children(ps.Procs)
	claimed := map[int]bool{}
	// The server is held back until the panes are counted: the panes are
	// its children, and each is reported on its own.
	if serverPID > 0 {
		claimed[serverPID] = true
	}
	var conch []int
	for _, p := range panes {
		if p.PID <= 0 { // 0 would be macOS's kernel, above everything
			continue
		}
		pids := procs.Tree(p.PID, children, ps.Procs, claimed)
		if len(pids) == 0 {
			continue
		}
		conch = append(conch, pids...)
		out.Panes = append(out.Panes, PaneResources{Pane: fleet.ComposePaneID(machine, p.ID),
			CPUCores: cores64(pids), Mem: ps.RSS(pids) * 1024, Procs: len(pids), Top: topProcs(before, ps, pids, rate)})
	}
	if serverPID > 0 {
		delete(claimed, serverPID)
		conch = append(conch, procs.Tree(serverPID, children, ps.Procs, claimed)...)
	}
	out.Conch = ResourceUse{CPUCores: cores64(conch), Mem: ps.RSS(conch) * 1024}
	out.CPUPercent = machineBusy(prev, cur, elapsed, rate, cores)
	return out
}

// machineBusy is the whole computer's CPU, 0–100. Linux counts it in
// /proc/stat. Elsewhere (macOS) it is every process's CPU time over the
// interval, out of all the cores' — which leaves out the kernel's own time
// and processes that came and went between the samples.
func machineBusy(prev, cur *machineSample, elapsed time.Duration, rate bool, cores int) *float64 {
	if prev == nil {
		return nil
	}
	if prev.ticksOK && cur.ticksOK {
		if pct, ok := procs.BusyPercent(prev.ticks, cur.ticks); ok {
			return round(pct, 10)
		}
		return nil
	}
	if !rate || cores <= 0 {
		return nil
	}
	all := make([]int, 0, len(cur.procs.Procs))
	for pid := range cur.procs.Procs {
		all = append(all, pid)
	}
	used := procs.Used(&prev.procs, &cur.procs, all)
	pct := float64(used) / float64(elapsed) / float64(cores) * 100
	return round(min(max(pct, 0), 100), 10)
}

// topProcs names the processes under a pane that used the most CPU,
// memory breaking ties (and deciding alone before there is a rate), the
// same name once. The pane's shell is left out while it runs something.
func topProcs(prev, cur *procs.Sample, pids []int, rate bool) []string {
	type use struct {
		name string
		cpu  time.Duration
		rss  int64
	}
	var uses []use
	at := map[string]int{}
	for i, pid := range pids {
		name := procName(cur.Procs[pid].Comm)
		if name == "" || i == 0 && len(pids) > 1 && isShell(name) {
			continue // pids[0] is the pane's own process
		}
		var cpu time.Duration
		if rate {
			cpu = procs.Spent(prev, cur, pid)
		}
		j, ok := at[name]
		if !ok {
			j = len(uses)
			at[name] = j
			uses = append(uses, use{name: name})
		}
		uses[j].cpu += cpu
		uses[j].rss += cur.Procs[pid].RSS
	}
	slices.SortStableFunc(uses, func(a, b use) int {
		if c := cmp.Compare(b.cpu, a.cpu); c != 0 {
			return c
		}
		if c := cmp.Compare(b.rss, a.rss); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	top := []string{}
	for _, u := range uses[:min(len(uses), 3)] {
		top = append(top, u.name)
	}
	return top
}

// procName is ps's comm as a person names the program: macOS gives the
// whole path, and a login shell is "-zsh".
func procName(comm string) string {
	if strings.TrimSpace(comm) == "" {
		return ""
	}
	return strings.TrimPrefix(filepath.Base(strings.TrimSpace(comm)), "-")
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "mksh", "tcsh", "csh", "nu", "elvish", "xonsh", "pwsh":
		return true
	}
	return false
}

// round keeps n decimal places' worth (n a power of ten), so the JSON says
// 0.153 rather than 0.15300000000000002.
func round(v float64, n float64) *float64 {
	r := math.Round(v*n) / n
	return &r
}
