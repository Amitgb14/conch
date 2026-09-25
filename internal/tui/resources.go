package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The resource monitor: an icon at the right end of the status bar opens a
// box with the memory and CPU conch uses on this computer — this TUI, the
// server, and each pane with everything running inside it. It samples ps
// while the box is open and not otherwise.

const (
	monitorIcon     = "▁▃▆"
	monitorEvery    = 2 * time.Second
	monitorMaxPanes = 8  // then the rest collapse into "N more"
	monitorNameW    = 26 // name column, in cells
	monitorBarW     = 10 // cells in a CPU bar; full is one core
)

// proc is one process from ps.
type proc struct {
	pid, ppid int
	rss       int64         // resident memory, KiB
	cpu       time.Duration // CPU time used so far
	comm      string
}

// procSample is every process on this computer at one moment.
type procSample struct {
	at    time.Time
	procs map[int]proc
	err   error
}

// runPS lists every process; tests replace it.
var runPS = func(ctx context.Context) ([]byte, error) {
	// The same fields exist in procps (Linux) and BSD ps (macOS); comm is
	// last because it may contain spaces.
	return exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,time=,comm=").Output()
}

// parsePS reads ps output, skipping lines it can't parse.
func parsePS(out []byte) map[int]proc {
	procs := map[int]proc{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		cpu, ok := parseCPUTime(f[3])
		if err1 != nil || err2 != nil || err3 != nil || !ok || pid < 0 || rss < 0 {
			continue
		}
		procs[pid] = proc{pid: pid, ppid: ppid, rss: rss, cpu: cpu, comm: strings.Join(f[4:], " ")}
	}
	return procs
}

// parseCPUTime reads ps's time column: [DD-][HH:]MM:SS[.ss]. Linux prints
// 00:01:02, macOS 1:02.34 (minutes may pass 59 there).
func parseCPUTime(s string) (time.Duration, bool) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 || math.IsInf(sec, 0) || math.IsNaN(sec) {
		return 0, false
	}
	total := sec + float64(days)*86400
	for i, mult := len(parts)-2, 60.0; i >= 0; i, mult = i-1, mult*60 {
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		total += float64(n) * mult
	}
	// Whole milliseconds, so float noise never shows up as CPU use.
	return time.Duration(math.Round(total*1000)) * time.Millisecond, true
}

// takeSample runs ps. A ps that fails but still prints its list (a process
// it couldn't read) is as good as one that didn't.
func takeSample() procSample {
	ctx, cancel := context.WithTimeout(context.Background(), monitorEvery)
	defer cancel()
	out, err := runPS(ctx)
	s := procSample{at: time.Now(), procs: parsePS(out)}
	if len(s.procs) == 0 {
		if err == nil {
			err = fmt.Errorf("ps listed no processes")
		}
		s.err = err
	}
	return s
}

// monitorSampleMsg carries a sample to the box that asked for it.
type monitorSampleMsg struct {
	mon    *monitor
	sample procSample
}

// monitorTickMsg asks the box for its next sample.
type monitorTickMsg struct{ mon *monitor }

// monitor is the box the status bar icon opens.
type monitor struct {
	prev, cur *procSample
}

func (m *Model) openMonitor() tea.Cmd {
	mon := &monitor{}
	m.overlay = mon
	return mon.sample()
}

func (mon *monitor) sample() tea.Cmd {
	return func() tea.Msg { return monitorSampleMsg{mon: mon, sample: takeSample()} }
}

// receiveMonitor stores a sample and schedules the next, only while the box
// that asked is still open.
func (m *Model) receiveMonitor(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case monitorSampleMsg:
		if m.overlay != msg.mon {
			return nil
		}
		s := msg.sample
		msg.mon.prev, msg.mon.cur = msg.mon.cur, &s
		mon := msg.mon
		return tea.Tick(monitorEvery, func(time.Time) tea.Msg { return monitorTickMsg{mon: mon} })
	case monitorTickMsg:
		if m.overlay != msg.mon {
			return nil
		}
		return msg.mon.sample()
	}
	return nil
}

func (mon *monitor) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

// mouse closes the box on any click, the icon's own included.
func (mon *monitor) mouse(m *Model, msg tea.MouseMsg, _ box) tea.Cmd {
	if msg.Action == tea.MouseActionPress {
		m.overlay = nil
	}
	return nil
}

// monUsage is what one row of the box adds up.
type monUsage struct {
	label  string
	rss    int64   // KiB
	cpu    float64 // percent of one core; <0 while unknown
	indent bool
}

// tree lists root and its descendants that aren't claimed yet, claiming
// them. Each pid is taken once, so a parent loop in ps's list (a pid reused
// while ps was reading) can't send it round forever.
func tree(root int, children map[int][]int, procs map[int]proc, claimed map[int]bool) []int {
	if _, ok := procs[root]; !ok || claimed[root] {
		return nil
	}
	var out []int
	stack := []int{root}
	claimed[root] = true
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, pid)
		for _, c := range children[pid] {
			if !claimed[c] {
				claimed[c] = true
				stack = append(stack, c)
			}
		}
	}
	return out
}

// sum adds up the memory and CPU of pids. CPU needs two samples: a process
// missing from the first, or whose time went down (its pid was reused),
// counts all its time as new.
func (mon *monitor) sum(pids []int) (rss int64, cpu float64) {
	cpu = -1
	var used time.Duration
	for _, pid := range pids {
		p := mon.cur.procs[pid]
		rss += p.rss
		d := p.cpu
		if mon.prev != nil {
			if old, ok := mon.prev.procs[pid]; ok && old.cpu <= p.cpu {
				d = p.cpu - old.cpu
			}
		}
		used += d
	}
	if mon.prev != nil && mon.prev.err == nil {
		if elapsed := mon.cur.at.Sub(mon.prev.at); elapsed > 0 {
			cpu = float64(used) / float64(elapsed) * 100
		}
	}
	return rss, cpu
}

// rows works out the box's rows from the latest sample. Only this
// computer's panes are matched: a remote pane's pid means another machine.
func (mon *monitor) rows(m Model) (rows []monUsage, total monUsage) {
	procs := mon.cur.procs
	children := map[int][]int{}
	for _, p := range procs {
		if p.ppid != p.pid {
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}
	for _, c := range children {
		slices.Sort(c)
	}
	claimed := map[int]bool{}
	var all []int
	add := func(label string, pids []int, indent bool) monUsage {
		all = append(all, pids...)
		rss, cpu := mon.sum(pids)
		return monUsage{label: label, rss: rss, cpu: cpu, indent: indent}
	}
	var local *machine
	for _, mach := range m.machines {
		if mach.id == localMachine {
			local = mach
		}
	}
	serverPID := 0
	if local != nil && local.server.PID > 0 {
		// Held back until the panes are counted: the server may be the TUI's
		// child (the TUI started it), and the panes are the server's.
		serverPID = local.server.PID
		claimed[serverPID] = true
	}
	// The TUI before the panes, so running it inside a conch pane doesn't
	// count it twice.
	rows = append(rows, add("This window (TUI)", tree(os.Getpid(), children, procs, claimed), false))

	var panes []monUsage
	if local != nil {
		for _, p := range local.panes {
			if p.PID <= 0 { // 0 would be macOS's kernel, above everything
				continue
			}
			if pids := tree(p.PID, children, procs, claimed); len(pids) > 0 {
				panes = append(panes, add(p.DisplayName(), pids, true))
			}
		}
	}
	if serverPID > 0 {
		if _, ok := procs[serverPID]; ok {
			delete(claimed, serverPID)
			rows = append(rows, add(fmt.Sprintf("Server · pid %d", serverPID), tree(serverPID, children, procs, claimed), false))
		}
	}
	slices.SortStableFunc(panes, func(a, b monUsage) int {
		switch {
		case a.rss > b.rss:
			return -1
		case a.rss < b.rss:
			return 1
		}
		return 0
	})
	// Adds up rows whose CPU may still be unknown (<0).
	sumRows := func(label string, us []monUsage, indent bool) monUsage {
		out := monUsage{label: label, cpu: -1, indent: indent}
		for _, u := range us {
			out.rss += u.rss
			if u.cpu >= 0 {
				out.cpu = max(out.cpu, 0) + u.cpu
			}
		}
		return out
	}
	head := sumRows(fmt.Sprintf("Panes (%d)", len(panes)), panes, false)
	if len(panes) > monitorMaxPanes+1 { // never a lone "1 more"
		rest := panes[monitorMaxPanes:]
		panes = append(panes[:monitorMaxPanes:monitorMaxPanes], sumRows(fmt.Sprintf("%d more", len(rest)), rest, true))
	}
	if local != nil {
		rows = append(rows, head)
		rows = append(rows, panes...)
	}
	rss, cpu := mon.sum(all)
	return rows, monUsage{label: "Total", rss: rss, cpu: cpu}
}

// formatMem shows KiB as KB, MB or GB.
func formatMem(kib int64) string {
	switch {
	case kib >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kib)/(1024*1024))
	case kib >= 1024:
		return fmt.Sprintf("%d MB", (kib+512)/1024)
	}
	return fmt.Sprintf("%d KB", kib)
}

// cpuStyle colours CPU use: fine, busy from 80% of a core, hot from two.
func cpuStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= 200:
		return styleErr
	case pct >= 80:
		return styleWarn
	}
	return styleOK
}

// cpuBar draws pct as a bar where full is one core.
func cpuBar(pct float64) string {
	n := int(math.Round(min(max(pct, 0), 100) / 100 * monitorBarW))
	return cpuStyle(pct).Render(strings.Repeat("█", n)) + styleMuted.Render(strings.Repeat("░", monitorBarW-n))
}

func (mon *monitor) render(m Model) box {
	var lines []string
	switch {
	case mon.cur == nil:
		lines = []string{" " + styleMuted.Render("reading processes…")}
	case mon.cur.err != nil:
		lines = []string{" " + styleErr.Render("ps: "+mon.cur.err.Error())}
	default:
		lines = append(lines, " "+styleMuted.Render(strings.Repeat(" ", monitorNameW)+fmt.Sprintf(" %8s %7s", "memory", "cpu")))
		rows, total := mon.rows(m)
		mem := lipgloss.NewStyle().Foreground(colorAccent)
		line := func(u monUsage, bold bool) string {
			name := u.label
			if u.indent {
				name = "  " + name
			}
			name = padRight(ansi.Truncate(name, monitorNameW, "…"), monitorNameW)
			cpu := styleMuted.Render(fmt.Sprintf("%7s", "…")) + " " + styleMuted.Render(strings.Repeat("░", monitorBarW))
			if u.cpu >= 0 {
				cpu = cpuStyle(u.cpu).Render(fmt.Sprintf("%6.1f%%", u.cpu)) + " " + cpuBar(u.cpu)
			}
			switch {
			case bold:
				name = styleBold.Render(name)
			case u.indent:
				name = styleMuted.Render(name)
			}
			return " " + name + " " + mem.Render(fmt.Sprintf("%8s", formatMem(u.rss))) + " " + cpu
		}
		for _, r := range rows {
			lines = append(lines, line(r, false))
		}
		lines = append(lines, line(total, true))
	}
	lines = append(lines, "",
		" "+styleMuted.Render(fmt.Sprintf("this computer · every %s · cpu 100%% = one core", monitorEvery)),
		" "+styleMuted.Render("any key closes"))
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l)+1)
	}
	if m.width > 2 {
		w = min(w, m.width-2)
	}
	b := box{lines: frameLines(" monitor ", lines, w, colorAccent)}
	b.x = max(m.width-b.width(), 0)
	b.y = max(m.height-statusHeight-len(b.lines), 0)
	return b
}

// monitorItem is the status bar icon: muted until its box is open.
func (m Model) monitorItem() statusItem {
	_, open := m.overlay.(*monitor)
	style := styleMuted
	if open {
		style = styleAccent
	}
	return statusItem{text: style.Render(monitorIcon), act: func(m *Model) tea.Cmd {
		if _, ok := m.overlay.(*monitor); ok {
			m.overlay = nil
			return nil
		}
		return m.openMonitor()
	}}
}
