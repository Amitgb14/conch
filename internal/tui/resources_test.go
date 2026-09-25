package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestMonitorParseCPUTime(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"00:00:00", 0},
		{"00:01:02", 62 * time.Second}, // Linux
		{"01:02:03", time.Hour + 2*time.Minute + 3*time.Second},
		{"2-01:00:00", 49 * time.Hour},                          // Linux, over a day
		{"0:00.03", 30 * time.Millisecond},                      // macOS
		{"0:00.29", 290 * time.Millisecond},                     // no float noise
		{"123:45.67", 123*time.Minute + 45670*time.Millisecond}, // macOS minutes pass 59
		{"1:02:03.50", time.Hour + 2*time.Minute + 3500*time.Millisecond},
	} {
		got, ok := parseCPUTime(c.in)
		if !ok || got != c.want {
			t.Errorf("parseCPUTime(%q) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "12", "1:2:3:4", "a:00", "00:xx", "-1:00", "x-00:00", "1:-5", "0:NaN", "0:+Inf", "1-2-00:00"} {
		if got, ok := parseCPUTime(bad); ok {
			t.Errorf("parseCPUTime(%q) = %v, want a failure", bad, got)
		}
	}
}

func TestMonitorParsePS(t *testing.T) {
	out := "    1     0  12000 00:00:05 systemd\n" + // Linux
		"  200     1   4096 0:01.50 /Applications/Some App.app/Contents/MacOS/Some App\n" + // macOS, spaces
		"garbage line\n" +
		"  300   200   notnum 0:00.00 x\n" +
		"  301   200   10 bad y\n" +
		"  -4    1   10 0:00.00 neg\n" +
		"  400   200   2048 0:00.00\n" + // no comm
		"\n"
	procs := parsePS([]byte(out))
	if len(procs) != 3 {
		t.Fatalf("parsed %d processes: %+v", len(procs), procs)
	}
	if p := procs[1]; p.ppid != 0 || p.rss != 12000 || p.cpu != 5*time.Second || p.comm != "systemd" {
		t.Errorf("pid 1: %+v", p)
	}
	if p := procs[200]; p.comm != "/Applications/Some App.app/Contents/MacOS/Some App" || p.cpu != 1500*time.Millisecond {
		t.Errorf("pid 200: %+v", p)
	}
	if p := procs[400]; p.rss != 2048 || p.comm != "" {
		t.Errorf("pid 400: %+v", p)
	}
	if got := parsePS(nil); len(got) != 0 {
		t.Errorf("nil: %v", got)
	}
	// An overlong line is skipped along with the rest; nothing panics.
	if got := parsePS([]byte(strings.Repeat("x", 2<<20))); len(got) != 0 {
		t.Errorf("overlong: %v", got)
	}
}

// The real ps, read-only: its format must parse on macOS and Linux alike.
func TestMonitorRealPS(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps")
	}
	s := takeSample()
	if s.err != nil {
		t.Fatal(s.err)
	}
	me, ok := s.procs[os.Getpid()]
	if !ok || me.rss <= 0 || me.ppid != os.Getppid() {
		t.Fatalf("this process: %+v (found %v) among %d", me, ok, len(s.procs))
	}
}

func fakePS(t *testing.T, out string, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	was := runPS
	runPS = func(context.Context) ([]byte, error) { calls.Add(1); return []byte(out), err }
	t.Cleanup(func() { runPS = was })
	return &calls
}

func TestMonitorTakeSample(t *testing.T) {
	fakePS(t, "1 0 10 0:00.00 init\n", errors.New("exit status 1"))
	if s := takeSample(); s.err != nil || len(s.procs) != 1 {
		t.Errorf("failed ps with a list: %+v", s)
	}
	fakePS(t, "", errors.New("exec: ps not found"))
	if s := takeSample(); s.err == nil || !strings.Contains(s.err.Error(), "not found") {
		t.Errorf("missing ps: %+v", s)
	}
	fakePS(t, "", nil)
	if s := takeSample(); s.err == nil {
		t.Errorf("empty list: %+v", s)
	}
}

// monSample builds a sample from "pid ppid rssKiB cpuMillis" rows.
func monSample(at time.Time, rows ...[4]int) *procSample {
	s := &procSample{at: at, procs: map[int]proc{}}
	for _, r := range rows {
		s.procs[r[0]] = proc{pid: r[0], ppid: r[1], rss: int64(r[2]), cpu: time.Duration(r[3]) * time.Millisecond}
	}
	return s
}

func monModel(serverPID int, panes ...proto.PaneInfo) Model {
	return Model{width: 120, height: 40, machines: []*machine{{id: localMachine, label: "local",
		server: proto.HelloResult{PID: serverPID}, panes: panes}}}
}

func find(rows []monUsage, label string) (monUsage, bool) {
	for _, r := range rows {
		if r.label == label {
			return r, true
		}
	}
	return monUsage{}, false
}

func TestMonitorRowsAndTotals(t *testing.T) {
	me := os.Getpid()
	t0 := time.Unix(1000, 0)
	m := monModel(100,
		proto.PaneInfo{ID: "p1", Name: "claude", PID: 200},
		proto.PaneInfo{ID: "p2", Name: "zsh", PID: 300})
	mon := &monitor{
		prev: monSample(t0, [4]int{me, 1, 30 * 1024, 0}, [4]int{100, 1, 20 * 1024, 0},
			[4]int{200, 100, 300 * 1024, 0}, [4]int{201, 200, 15 * 1024, 0}, [4]int{300, 100, 4 * 1024, 0}),
		cur: monSample(t0.Add(2*time.Second), [4]int{me, 1, 30 * 1024, 1000}, [4]int{100, 1, 20 * 1024, 200},
			[4]int{200, 100, 300 * 1024, 2000}, [4]int{201, 200, 15 * 1024, 1000}, [4]int{300, 100, 4 * 1024, 0},
			[4]int{999, 1, 1 << 20, 50000}), // not conch's
	}
	rows, total := mon.rows(m)
	want := []struct {
		label string
		mb    int64
		cpu   float64
	}{
		{"This window (TUI)", 30, 50},
		{"Server · pid 100", 20, 10},
		{"Panes (2)", 319, 150},
		{"claude", 315, 150},
		{"zsh", 4, 0},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i, w := range want {
		if rows[i].label != w.label || rows[i].rss != w.mb*1024 || rows[i].cpu != w.cpu {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
	if total.rss != 369*1024 || total.cpu != 210 {
		t.Errorf("total %+v", total)
	}
	// One sample: memory, but no CPU yet.
	mon.prev = nil
	rows, total = mon.rows(m)
	if rows[0].cpu >= 0 || total.cpu >= 0 || total.rss != 369*1024 {
		t.Errorf("first sample: %+v %+v", rows[0], total)
	}
	got := ansi.Strip(strings.Join(mon.render(m).lines, "\n"))
	if !strings.Contains(got, "…") || !strings.Contains(got, "369 MB") {
		t.Errorf("first sample render:\n%s", got)
	}
}

func TestMonitorClock(t *testing.T) {
	me := os.Getpid()
	t0 := time.Unix(1000, 0)
	m := monModel(0)
	for _, d := range []time.Duration{0, -time.Second} {
		mon := &monitor{prev: monSample(t0, [4]int{me, 1, 10, 0}), cur: monSample(t0.Add(d), [4]int{me, 1, 10, 500})}
		if _, total := mon.rows(m); total.cpu >= 0 {
			t.Errorf("elapsed %v: cpu %v", d, total.cpu)
		}
	}
	// A failed sample before leaves CPU unknown rather than wild.
	mon := &monitor{prev: &procSample{at: t0, err: errors.New("x")}, cur: monSample(t0.Add(time.Second), [4]int{me, 1, 10, 500})}
	if _, total := mon.rows(m); total.cpu >= 0 {
		t.Errorf("after an error: cpu %v", total.cpu)
	}
}

// A pid reused between samples: the new process's time is all new, and
// never subtracts from its neighbours.
func TestMonitorReusedPIDBetweenSamples(t *testing.T) {
	me := os.Getpid()
	t0 := time.Unix(1000, 0)
	m := monModel(100, proto.PaneInfo{ID: "p1", Name: "sh", PID: 200})
	mon := &monitor{
		prev: monSample(t0, [4]int{me, 1, 1, 0}, [4]int{100, 1, 1, 0}, [4]int{200, 100, 1, 1000}, [4]int{201, 200, 1, 90000}),
		cur:  monSample(t0.Add(time.Second), [4]int{me, 1, 1, 0}, [4]int{100, 1, 1, 0}, [4]int{200, 100, 1, 1500}, [4]int{201, 200, 1, 100}),
	}
	rows, _ := mon.rows(m)
	if r, ok := find(rows, "sh"); !ok || r.cpu != 60 {
		t.Errorf("pane: %+v", r)
	}
}

// ps lists processes one by one, so a pid reused mid-listing can make two
// processes each other's parent. Adding them up must still end.
func TestMonitorParentLoop(t *testing.T) {
	me := os.Getpid()
	m := monModel(100, proto.PaneInfo{ID: "p1", Name: "sh", PID: 200})
	mon := &monitor{cur: monSample(time.Unix(1, 0), [4]int{me, 1, 1, 0}, [4]int{100, 1, 1, 0},
		[4]int{200, 201, 7, 0}, [4]int{201, 200, 5, 0}, [4]int{202, 202, 3, 0}, [4]int{203, 202, 1, 0})}
	done := make(chan []monUsage)
	go func() { rows, _ := mon.rows(m); done <- rows }()
	select {
	case rows := <-done:
		if _, ok := find(rows, "Server · pid 100"); !ok {
			t.Errorf("rows %+v", rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rows never returned on a parent loop")
	}
	// tree on its own, straight into a loop.
	claimed := map[int]bool{}
	got := tree(200, map[int][]int{200: {201}, 201: {200}}, mon.cur.procs, claimed)
	if len(got) != 2 {
		t.Errorf("loop tree: %v", got)
	}
}

func TestMonitorEdgeMachinesAndPIDs(t *testing.T) {
	me := os.Getpid()
	cur := monSample(time.Unix(1, 0), [4]int{0, 0, 0, 0}, [4]int{1, 0, 50 * 1024, 0}, [4]int{me, 1, 1024, 0},
		[4]int{100, me, 2048, 0}, [4]int{200, 100, 4096, 0}, [4]int{500, 1, 8192, 0})

	// No machines at all: just this window, with every process under it.
	mon := &monitor{cur: cur}
	rows, total := mon.rows(Model{})
	if len(rows) != 1 || total.rss != 1024+2048+4096 {
		t.Errorf("no machines: %+v %+v", rows, total)
	}

	// The server started by this TUI is its child: it still gets its own row,
	// and the panes theirs.
	m := monModel(100,
		proto.PaneInfo{ID: "p0", Name: "zero", PID: 0}, // would be macOS's kernel
		proto.PaneInfo{ID: "p1", Name: "sh", PID: 200},
		proto.PaneInfo{ID: "p2", Name: "twin", PID: 200}, // same pid twice
		proto.PaneInfo{ID: "p3", Name: "gone", PID: 777}) // exited since
	// A remote machine's pane with a pid that exists here is not ours.
	m.machines = append(m.machines, &machine{id: "far", panes: []proto.PaneInfo{{ID: "r1", Name: "remote", PID: 500}}})
	rows, total = mon.rows(m)
	if r, _ := find(rows, "This window (TUI)"); r.rss != 1024 {
		t.Errorf("TUI swallowed its child server: %+v", r)
	}
	if r, _ := find(rows, "Server · pid 100"); r.rss != 2048 {
		t.Errorf("server: %+v", r)
	}
	for _, l := range []string{"zero", "twin", "gone", "remote"} {
		if _, ok := find(rows, l); ok {
			t.Errorf("row %q listed: %+v", l, rows)
		}
	}
	if r, _ := find(rows, "Panes (1)"); r.rss != 4096 {
		t.Errorf("panes: %+v", rows)
	}
	if total.rss != 1024+2048+4096 {
		t.Errorf("total %d", total.rss)
	}

	// The TUI running inside a conch pane is counted once.
	cur = monSample(time.Unix(1, 0), [4]int{100, 1, 2048, 0}, [4]int{200, 100, 4096, 0}, [4]int{me, 200, 1024, 0})
	mon = &monitor{cur: cur}
	rows, total = mon.rows(monModel(100, proto.PaneInfo{ID: "p1", Name: "zsh", PID: 200}))
	if r, _ := find(rows, "zsh"); r.rss != 4096 || total.rss != 1024+2048+4096 {
		t.Errorf("TUI in a pane: %+v total %d", rows, total.rss)
	}
	// A server that isn't running here gets no row.
	rows, _ = mon.rows(monModel(4242))
	if _, ok := find(rows, "Server · pid 4242"); ok {
		t.Errorf("missing server listed: %+v", rows)
	}
}

func TestMonitorManyPanesCollapse(t *testing.T) {
	me := os.Getpid()
	for _, n := range []int{8, 9, 10, 30} {
		rows := [][4]int{{me, 1, 1, 0}, {100, 1, 1, 0}}
		var panes []proto.PaneInfo
		for i := range n {
			pid := 1000 + i
			rows = append(rows, [4]int{pid, 100, (i + 1) * 1024, 0})
			panes = append(panes, proto.PaneInfo{ID: strconv.Itoa(i), Name: fmt.Sprintf("pane%d", i), PID: pid})
		}
		mon := &monitor{cur: monSample(time.Unix(1, 0), rows...)}
		got, _ := mon.rows(monModel(100, panes...))
		listed := got[3:] // TUI, server, Panes (n)
		wantRows := n
		if n > monitorMaxPanes+1 {
			wantRows = monitorMaxPanes + 1
		}
		if len(listed) != wantRows || got[2].label != fmt.Sprintf("Panes (%d)", n) {
			t.Fatalf("%d panes: %d rows %+v", n, len(listed), listed)
		}
		if listed[0].label != fmt.Sprintf("pane%d", n-1) { // largest first
			t.Errorf("%d panes: first %q", n, listed[0].label)
		}
		if n > monitorMaxPanes+1 {
			last := listed[len(listed)-1]
			if last.label != fmt.Sprintf("%d more", n-monitorMaxPanes) {
				t.Errorf("%d panes: last %q", n, last.label)
			}
			var sum int64
			for _, r := range listed {
				sum += r.rss
			}
			if sum != got[2].rss {
				t.Errorf("%d panes: rows add to %d, heading says %d", n, sum, got[2].rss)
			}
		}
	}
}

func TestMonitorRenderSizes(t *testing.T) {
	me := os.Getpid()
	t0 := time.Unix(1000, 0)
	panes := []proto.PaneInfo{
		{ID: "p1", Name: strings.Repeat("a very long pane name ", 5), PID: 200},
		{ID: "p2", Name: "日本語のとても長いペインの名前です", PID: 300},
	}
	mon := &monitor{
		prev: monSample(t0, [4]int{me, 1, 1, 0}, [4]int{100, 1, 1, 0}, [4]int{200, 100, 1, 0}, [4]int{300, 100, 1, 0}),
		cur:  monSample(t0.Add(time.Second), [4]int{me, 1, 1, 10}, [4]int{100, 1, 1, 0}, [4]int{200, 100, 5 << 20, 3000}, [4]int{300, 100, 900, 0}),
	}
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 10}, {60, 20}, {120, 40}, {300, 100}} {
		m := monModel(100, panes...)
		m.width, m.height = size[0], size[1]
		m.overlay = mon
		b := mon.render(m)
		for i, l := range b.lines {
			if w := ansi.StringWidth(l); w != b.width() || b.x+w > max(m.width, b.width()) {
				t.Errorf("%v: line %d is %d wide (box %d at x %d)", size, i, w, b.width(), b.x)
			}
		}
		for i, l := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(l); w > m.width {
				t.Errorf("%v: screen line %d is %d wide", size, i, w)
			}
		}
	}
	m := monModel(100, panes...)
	got := ansi.Strip(strings.Join(mon.render(m).lines, "\n"))
	for _, want := range []string{"5.0 GB", "900 KB", "300.0%", "…", "Total", "cpu 100% = one core", "any key closes"} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	// Every row keeps the columns aligned, however long or wide its name.
	col := -1
	for _, l := range strings.Split(got, "\n") {
		if i := strings.Index(l, " MB ") + strings.Index(l, " GB ") + strings.Index(l, " KB ") + 2; strings.Contains(l, "B ") && i > 0 {
			w := ansi.StringWidth(l[:i])
			if col >= 0 && w != col {
				t.Errorf("misaligned: %q", l)
			}
			col = w
		}
	}

	// Before the first sample, and when ps fails.
	if got := ansi.Strip(strings.Join((&monitor{}).render(m).lines, "\n")); !strings.Contains(got, "reading processes") {
		t.Errorf("waiting:\n%s", got)
	}
	failed := &monitor{cur: &procSample{err: errors.New("exec: \"ps\": not found")}}
	if got := ansi.Strip(strings.Join(failed.render(m).lines, "\n")); !strings.Contains(got, "ps: exec") {
		t.Errorf("error:\n%s", got)
	}
}

func TestMonitorSamplingStopsWhenClosed(t *testing.T) {
	calls := fakePS(t, fmt.Sprintf("%d 1 1024 0:00.00 conch\n", os.Getpid()), nil)
	m := monModel(0)
	cmd := m.openMonitor()
	mon := m.overlay.(*monitor)
	msg := cmd()
	if calls.Load() != 1 {
		t.Fatalf("ps ran %d times", calls.Load())
	}
	next, tick := m.Update(msg)
	m = next.(Model)
	if mon.cur == nil || tick == nil {
		t.Fatal("sample not stored or no tick scheduled")
	}
	// The tick asks for another sample while the box is open.
	next, cmd = m.Update(monitorTickMsg{mon: mon})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("no sample after a tick")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if mon.prev == nil || calls.Load() != 2 {
		t.Fatalf("second sample: prev %v, %d calls", mon.prev, calls.Load())
	}

	// Any key closes it; late ticks and samples then do nothing, whatever
	// else is open.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if m.overlay != nil {
		t.Fatalf("key left %T open", m.overlay)
	}
	for _, other := range []overlay{nil, newHelp(), newVersionInfo(), &monitor{}} {
		m.overlay = other
		for _, late := range []tea.Msg{monitorTickMsg{mon: mon}, monitorSampleMsg{mon: mon, sample: procSample{}}} {
			if _, cmd := m.Update(late); cmd != nil {
				t.Errorf("late %T with %T open gave a command", late, other)
			}
		}
	}
	if calls.Load() != 2 {
		t.Errorf("ps ran after closing: %d", calls.Load())
	}
}

// The icon sits at the right end: a click opens the box, another closes it.
func TestMonitorIconClicks(t *testing.T) {
	fakePS(t, "", nil)
	for _, w := range []int{20, 40, 80, 120, 200} {
		m := monModel(0)
		m.brain = newBrainState()
		m.width, m.height = w, 10
		line, hits := m.layoutStatus()
		plain := ansi.Strip(line)
		if !strings.HasSuffix(strings.TrimRight(plain, " "), monitorIcon) {
			t.Errorf("width %d: icon not in the corner: %q", w, plain)
			continue
		}
		x := hits[len(hits)-1].x0
		click := func(a tea.MouseAction) {
			next, _ := m.handleMouse(tea.MouseMsg{X: x, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: a})
			m = next.(Model)
		}
		click(tea.MouseActionRelease)
		if m.overlay != nil {
			t.Fatalf("width %d: release opened %T", w, m.overlay)
		}
		click(tea.MouseActionPress)
		if _, ok := m.overlay.(*monitor); !ok {
			t.Fatalf("width %d: click opened %T", w, m.overlay)
		}
		click(tea.MouseActionRelease)
		if _, ok := m.overlay.(*monitor); !ok {
			t.Fatalf("width %d: release closed it", w)
		}
		click(tea.MouseActionPress)
		if m.overlay != nil {
			t.Fatalf("width %d: second click left %T", w, m.overlay)
		}
	}
	// Its own action toggles too.
	m := monModel(0)
	it := m.monitorItem()
	it.act(&m)
	if _, ok := m.overlay.(*monitor); !ok {
		t.Fatal("act did not open")
	}
	m.monitorItem().act(&m)
	if m.overlay != nil {
		t.Fatal("act did not close")
	}
}

// fg is the escape lipgloss writes for a foreground colour (it rounds some
// colours by one, so it is asked rather than worked out here).
func fg(c lipgloss.Color) string {
	return regexp.MustCompile(`38;2;\d+;\d+;\d+`).FindString(lipgloss.NewStyle().Foreground(c).Render("x"))
}

func TestMonitorColoursFollowTheme(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	me := os.Getpid()
	t0 := time.Unix(1000, 0)
	mon := &monitor{
		prev: monSample(t0, [4]int{me, 1, 1024, 0}, [4]int{100, 1, 1024, 0}, [4]int{200, 100, 1024, 0}),
		cur:  monSample(t0.Add(time.Second), [4]int{me, 1, 1024, 100}, [4]int{100, 1, 1024, 900}, [4]int{200, 100, 1024, 2500}),
	}
	m := monModel(100, proto.PaneInfo{ID: "p1", Name: "busy", PID: 200})
	for _, name := range []string{"conch", "dracula", "gruvbox"} {
		applyTheme(name, "")
		th := themeByName(name)
		if fg(th.accent) == "" || fg(th.muted) == "" {
			t.Fatalf("%s: no colour escapes to look for", name)
		}
		lines := mon.render(m).lines
		row := func(label string) string {
			for _, l := range lines {
				if strings.Contains(ansi.Strip(l), label) {
					return l
				}
			}
			t.Fatalf("%s: no %q row", name, label)
			return ""
		}
		tui, server, busy := row("This window"), row("Server"), row("busy")
		for _, c := range []struct {
			what, line string
			want       lipgloss.Color
		}{
			{"memory", tui, th.accent},
			{"10% cpu", tui, th.ok},
			{"90% cpu", server, th.warn},
			{"250% cpu", busy, th.err},
			{"unfilled bar", tui, th.muted},
			{"pane name", busy, th.muted},
			{"border", lines[0], th.accent},
		} {
			if !strings.Contains(c.line, fg(c.want)) {
				t.Errorf("%s: %s not in %s: %q", name, c.what, c.want, c.line)
			}
		}

		// The icon: muted when closed, the accent while its box is open.
		m.overlay = nil
		if got := m.monitorItem().text; !strings.Contains(got, fg(th.muted)) {
			t.Errorf("%s: closed icon %q", name, got)
		}
		m.overlay = newVersionInfo()
		if got := m.monitorItem().text; !strings.Contains(got, fg(th.muted)) {
			t.Errorf("%s: another box lit the icon: %q", name, got)
		}
		m.overlay = mon
		if got := m.monitorItem().text; !strings.Contains(got, fg(th.accent)) {
			t.Errorf("%s: open icon %q", name, got)
		}
	}
}

func TestMonitorFormatMem(t *testing.T) {
	for kib, want := range map[int64]string{0: "0 KB", 1023: "1023 KB", 1024: "1 MB", 1536: "2 MB", 1024 * 1024: "1.0 GB", 3 * 1024 * 1024 / 2: "1.5 GB"} {
		if got := formatMem(kib); got != want {
			t.Errorf("formatMem(%d) = %q, want %q", kib, got, want)
		}
	}
}
