package tui

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/procs"
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

// takeSample runs ps, given until the next sample is due.
func takeSample() procs.Sample { return procs.Take(monitorEvery) }

// monitorSampleMsg carries a sample to the box that asked for it.
type monitorSampleMsg struct {
	mon    *monitor
	sample procs.Sample
}

// monitorTickMsg asks the box for its next sample.
type monitorTickMsg struct{ mon *monitor }

// monitor is the box the status bar icon opens.
type monitor struct {
	prev, cur *procs.Sample
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

// sum adds up the memory and CPU of pids. CPU needs two samples.
func (mon *monitor) sum(pids []int) (rss int64, cpu float64) {
	cpu = -1
	if elapsed, ok := procs.Elapsed(mon.prev, mon.cur); ok {
		cpu = float64(procs.Used(mon.prev, mon.cur, pids)) / float64(elapsed) * 100
	}
	return mon.cur.RSS(pids), cpu
}

// rows works out the box's rows from the latest sample. Only this
// computer's panes are matched: a remote pane's pid means another machine.
func (mon *monitor) rows(m Model) (rows []monUsage, total monUsage) {
	running := mon.cur.Procs
	children := procs.Children(running)
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
	rows = append(rows, add("This window (TUI)", procs.Tree(os.Getpid(), children, running, claimed), false))

	var panes []monUsage
	if local != nil {
		for _, p := range local.panes {
			if p.PID <= 0 { // 0 would be macOS's kernel, above everything
				continue
			}
			if pids := procs.Tree(p.PID, children, running, claimed); len(pids) > 0 {
				panes = append(panes, add(p.DisplayName(), pids, true))
			}
		}
	}
	if serverPID > 0 {
		if _, ok := running[serverPID]; ok {
			delete(claimed, serverPID)
			rows = append(rows, add(fmt.Sprintf("Server · pid %d", serverPID), procs.Tree(serverPID, children, running, claimed), false))
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
	case mon.cur.Err != nil:
		lines = []string{" " + styleErr.Render("ps: "+mon.cur.Err.Error())}
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
