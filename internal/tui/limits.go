package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func (mach *machine) setLimits(l proto.PlanLimits) {
	if mach.limits == nil {
		mach.limits = map[string]proto.PlanLimits{}
	}
	mach.limits[l.Agent] = l
}

// liveWindow drops a window whose reset time has passed: its usage is stale.
func liveWindow(w *proto.LimitWindow, now time.Time) *proto.LimitWindow {
	if w == nil || (!w.ResetsAt.IsZero() && now.After(w.ResetsAt)) {
		return nil
	}
	return w
}

func pctStyle(p float64) func(...string) string {
	switch {
	case p >= 90:
		return styleErr.Render
	case p >= 70:
		return styleWarn.Render
	}
	return styleMuted.Render
}

// limitsChip is the compact status bar form, e.g. "Claude 5h 42% · 7d 18%".
func limitsChip(l proto.PlanLimits, now time.Time) string {
	var parts []string
	for _, w := range []struct {
		name string
		win  *proto.LimitWindow
	}{{"5h", l.FiveHour}, {"7d", l.Week}, {"spend", l.Spend}} {
		if lw := liveWindow(w.win, now); lw != nil {
			parts = append(parts, pctStyle(lw.UsedPct)(fmt.Sprintf("%s %d%%", w.name, int(math.Round(lw.UsedPct)))))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return styleMuted.Render(shortAgent(l.Agent)+" ") + strings.Join(parts, styleMuted.Render(" · "))
}

func shortAgent(agent string) string {
	switch agent {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	}
	return agentLabel(agent)
}

// statusLimits is the status bar item for the machine in view: the limits
// of the viewed agent, else of any agent there.
func (m Model) statusLimits(now time.Time) []statusItem {
	pl := m.contextPlace()
	mach := m.machine(pl.machine)
	if mach == nil || len(mach.limits) == 0 {
		return nil
	}
	agent := ""
	if r, ok := m.selectedRow(); ok && r.kind == kindPane {
		if p := m.pane(r.machine, r.paneID); p != nil && p.Agent != nil {
			agent = p.Agent.Name
		}
	}
	order := []string{agent, "claude", "codex"}
	for _, a := range order {
		l, ok := mach.limits[a]
		if !ok {
			continue
		}
		if chip := limitsChip(l, now); chip != "" {
			lim := l
			return []statusItem{{text: chip, act: func(m *Model) tea.Cmd {
				m.setFlash(limitsDetail(lim, time.Now()), false)
				return nil
			}}}
		}
	}
	return nil
}

// limitsDetail spells the windows out with reset times.
func limitsDetail(l proto.PlanLimits, now time.Time) string {
	var parts []string
	for _, w := range []struct {
		name string
		win  *proto.LimitWindow
	}{{"5-hour", l.FiveHour}, {"week", l.Week}, {"spend", l.Spend}} {
		lw := liveWindow(w.win, now)
		if lw == nil {
			continue
		}
		s := fmt.Sprintf("%s %d%% used", w.name, int(math.Round(lw.UsedPct)))
		if !lw.ResetsAt.IsZero() {
			s += ", resets " + resetText(lw.ResetsAt, now)
		}
		parts = append(parts, s)
	}
	return agentLabel(l.Agent) + ": " + strings.Join(parts, " · ")
}

func resetText(t, now time.Time) string {
	d := t.Sub(now)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes())+1)
	case d < 24*time.Hour && t.Day() == now.Day():
		return t.Format("15:04")
	case d < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 2 15:04")
}

// limitsLines is the plan limits block of a machine page, with bars.
func (m Model) limitsLines(mach *machine, w int) []string {
	now := time.Now()
	var lines []string
	for _, agent := range []string{"claude", "codex"} {
		l, ok := mach.limits[agent]
		if !ok {
			continue
		}
		var rows []string
		for _, win := range []struct {
			name string
			w    *proto.LimitWindow
		}{{"5-hour", l.FiveHour}, {"week", l.Week}, {"spend", l.Spend}} {
			lw := liveWindow(win.w, now)
			if lw == nil {
				continue
			}
			row := fmt.Sprintf("  %-7s %s %3d%%", win.name, bar(lw.UsedPct, 20), int(math.Round(lw.UsedPct)))
			if !lw.ResetsAt.IsZero() {
				row += styleMuted.Render("  resets " + resetText(lw.ResetsAt, now))
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			continue
		}
		lines = append(lines, "", styleBold.Render(agentLabel(agent)+" plan usage")+styleMuted.Render("  as of "+ago(l.At)))
		lines = append(lines, rows...)
	}
	return lines
}

// bar draws a usage bar n cells wide.
func bar(pct float64, n int) string {
	filled := int(math.Round(math.Min(math.Max(pct, 0), 100) / 100 * float64(n)))
	return pctStyleBar(pct)(strings.Repeat("█", filled)) + styleMuted.Render(strings.Repeat("░", n-filled))
}

func pctStyleBar(p float64) func(...string) string {
	switch {
	case p >= 90:
		return styleErr.Render
	case p >= 70:
		return styleWarn.Render
	}
	return styleOK.Render
}
