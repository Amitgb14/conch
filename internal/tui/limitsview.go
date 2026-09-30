package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amitgb14/conch/internal/proto"
)

// The plan usage window: what the status bar's "Claude 5h 42% · 7d 18%"
// says, in full. The chip has room for two numbers; the questions people
// actually have — when does it reset, which account is it, what has conch
// itself seen this machine spend — need more than a line that fades.
//
// Opened by clicking the chip, closed by clicking it again or esc, the way
// the resources window opens from its own icon.

type limitsView struct {
	mid  string
	fill float64 // 0..1 while the bars grow, 1 once they are there
}

// The bars fill when the window opens rather than appearing full: eight
// steps over a quarter of a second, then it stops. A gauge that grows says
// which of them is long without anybody reading the numbers, and a one-shot
// costs eight repaints of a small box — nothing keeps ticking afterwards.
const (
	limitsFillSteps = 8
	limitsFillEvery = 30 * time.Millisecond
)

type limitsFillMsg struct{ v *limitsView }

func (m *Model) openLimits(mid string) tea.Cmd {
	v := &limitsView{mid: mid}
	m.overlay = v
	return v.grow()
}

func (v *limitsView) grow() tea.Cmd {
	return tea.Tick(limitsFillEvery, func(time.Time) tea.Msg { return limitsFillMsg{v: v} })
}

func (v *limitsView) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case limitsFillMsg:
		if msg.v != v { // a window opened and closed before its tick arrived
			return false, nil
		}
		v.fill += 1.0 / limitsFillSteps
		if v.fill >= 1 {
			v.fill = 1
			return false, nil
		}
		return false, v.grow()
	case tea.KeyMsg:
		// The key path throws the closed flag away (handleKey), so an
		// overlay takes itself off the screen or nothing does. Any key
		// closes this one: it is a window to read, not to work in.
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

func (v *limitsView) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action == tea.MouseActionPress && !b.contains(msg.X, msg.Y) {
		m.overlay = nil
	}
	return nil
}

// limitsWindowLines is one machine's block: every agent that has reported,
// every window it reported, and what conch has seen the machine spend.
func (m Model) limitsWindowLines(mach *machine, now time.Time, w int, grown float64) []string {
	var lines []string
	agents := knownAgents(&m)
	seen := map[string]bool{}
	for _, agent := range agents {
		l, ok := mach.limits[agent]
		if !ok || seen[agent] {
			continue
		}
		seen[agent] = true
		var rows []string
		for _, win := range planWindows(l) {
			lw := win.w
			if lw == nil {
				continue
			}
			pct := int(math.Round(lw.UsedPct))
			row := fmt.Sprintf("   %-11s %s %3d%%", win.name, usageBar(lw.UsedPct*grown, 24), pct)
			switch {
			case lw.UsedPct >= 100:
				row += styleErr.Render("  used up")
			case liveWindow(lw, now) == nil:
				// Its reset time has passed, so the number is stale: the
				// agent has not reported since the window turned over.
				row += styleMuted.Render("  window has turned over since")
			}
			if !lw.ResetsAt.IsZero() {
				row += styleMuted.Render("  resets " + resetText(lw.ResetsAt, now) +
					" (" + lw.ResetsAt.Local().Format("Mon 15:04") + ")")
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			rows = []string{styleMuted.Render("   nothing reported yet")}
		}
		head := " " + styleBold.Render(agentLabel(agent))
		if !l.At.IsZero() {
			head += styleMuted.Render("  as the agent last said, " + ago(l.At))
		}
		lines = append(lines, head)
		lines = append(lines, rows...)
		lines = append(lines, "")
	}
	if len(lines) == 0 {
		return nil
	}
	// What conch has seen, which is a different thing from the plan window
	// and is worth saying so plainly.
	if u := m.machineUsage(mach.id); !u.empty() {
		// What this counts, exactly, because it will be read beside the
		// agent's own panel and the two are easy to mistake for each other:
		// every conversation in full, as each agent counts it, not the run
		// it happens to be on now.
		lines = append(lines, " "+styleBold.Render("Seen by conch here")+
			styleMuted.Render("  whole conversations, as each agent counts them"))
		lines = append(lines, fmt.Sprintf("   %s across %s", u.chip(), count(u.agents, "agent")))
		if u.output > 0 || u.input > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("   %s prompt · %s cache read · %s cache write · %s out",
				humanCount(u.prompt), humanCount(u.cacheRead), humanCount(u.cacheWrite), humanCount(u.output))))
		}
		lines = append(lines, "")
	}
	return lines
}

func (v *limitsView) render(m Model) box {
	now := time.Now()
	grown := v.fill
	if grown <= 0 {
		grown = 1.0 / limitsFillSteps // never a row of nothing
	}
	w := clamp(76, 20, max(m.width-2, 20))
	var lines []string

	// The machine whose chip was clicked first, then every other machine
	// that has reported something, so a fleet's limits are in one place
	// rather than one chip at a time. A machine with nothing to say is left
	// out entirely rather than given an empty heading.
	add := func(mach *machine) {
		if mach == nil {
			return
		}
		block := m.limitsWindowLines(mach, now, w, grown)
		if len(block) == 0 {
			return
		}
		lines = append(lines, " "+styleBold.Render(mach.label)+styleMuted.Render("  "+machineWhere(m, mach)))
		lines = append(lines, block...)
	}
	mach := m.machine(v.mid)
	add(mach)
	for _, other := range m.machines {
		if other != mach {
			add(other)
		}
	}
	if len(lines) == 0 {
		lines = []string{" " + styleMuted.Render("no agent here has reported a plan window yet"), ""}
	}
	lines = append(lines, styleMuted.Render(" esc close"))
	// Above the chip it was opened from, at the bottom right, the way the
	// resources window sits under its own icon: a status bar thing belongs
	// by the status bar, not in the middle of the work.
	b := box{lines: frameLines(" Plan usage ", lines, w, colorAccent)}
	b.x = max(m.width-b.width(), 0)
	b.y = max(m.height-statusHeight-len(b.lines), 0)
	return b
}

// machineWhere is the one-line "where is this" for a machine's heading.
func machineWhere(m Model, mach *machine) string {
	if mach.id == localMachine {
		return "this computer"
	}
	if mach.target != "" {
		return mach.target
	}
	return mach.id
}

// usageBar draws a usage bar n cells wide out of coloured cells rather than
// block glyphs: █ measures one cell and some terminals draw it two, which
// would push everything after it out of line.
func usageBar(pct float64, n int) string {
	filled := int(math.Round(math.Min(math.Max(pct, 0), 100) / 100 * float64(n)))
	full := barStyle(pct).Render(strings.Repeat(" ", filled))
	return full + styleTrack.Render(strings.Repeat(" ", n-filled))
}

// barStyle colours a usage bar the way the percentages beside it are
// coloured: quiet until it matters.
func barStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= 90:
		return styleBarErr
	case pct >= 70:
		return styleBarWarn
	}
	return styleBarOK
}

// planWindow is one window to draw: the agent's own name for it, made
// readable, and the numbers.
type planWindow struct {
	name string
	w    *proto.LimitWindow
}

// planWindows is every window an agent reported. The named list is what the
// agent itself sent, so an account with a per-model allowance shows that
// window too; a server or agent too old to send one falls back to the three
// conch has always known.
func planWindows(l proto.PlanLimits) []planWindow {
	if len(l.Windows) > 0 {
		out := make([]planWindow, 0, len(l.Windows))
		for _, nw := range l.Windows {
			w := &proto.LimitWindow{UsedPct: nw.UsedPct, ResetsAt: nw.ResetsAt}
			out = append(out, planWindow{name: windowName(nw.Key), w: w})
		}
		return out
	}
	return []planWindow{{"5-hour", l.FiveHour}, {"week", l.Week}, {"spend", l.Spend}}
}

// windowName makes an agent's key readable without pretending to know
// every key it may invent: the ones in use are spelled out, and anything
// else is shown as the agent wrote it, with its underscores opened up.
func windowName(key string) string {
	switch key {
	case "five_hour":
		return "5-hour"
	case "seven_day":
		return "week"
	case "spend_limit":
		return "spend"
	}
	if rest, ok := strings.CutPrefix(key, "seven_day_"); ok {
		return "week (" + strings.ToUpper(rest[:1]) + rest[1:] + ")"
	}
	return strings.ReplaceAll(key, "_", " ")
}
