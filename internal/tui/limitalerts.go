package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Plan limit alerts fire once per threshold per limit window: the window is
// identified by its machine, agent, name and reset time, so a new window
// (after a reset) alerts again, and so does usage that drops below every
// threshold and climbs back. What has fired is saved in ui.json, so
// restarting the TUI doesn't repeat alerts.

type limitAlert struct {
	machine   string
	agent     string
	window    string // "5-hour", "week" or "spend"
	pct       float64
	threshold int
	resets    time.Time
}

// limitKey names one window of one agent's plan on one machine.
func limitKey(mid, agent, window string, resets time.Time) string {
	unix := int64(0)
	if !resets.IsZero() {
		unix = resets.Unix()
	}
	return mid + "|" + agent + "|" + window + "|" + strconv.FormatInt(unix, 10)
}

// limitCrossings updates seen (key → highest threshold alerted) with l and
// returns the alerts to raise: at most one per window, for the highest
// threshold passed since the last alert.
func limitCrossings(seen map[string]int, mid string, l proto.PlanLimits, thresholds []int, now time.Time) []limitAlert {
	var out []limitAlert
	for _, w := range []struct {
		name string
		win  *proto.LimitWindow
	}{{"5-hour", l.FiveHour}, {"week", l.Week}, {"spend", l.Spend}} {
		lw := liveWindow(w.win, now)
		if lw == nil {
			continue
		}
		key := limitKey(mid, l.Agent, w.name, lw.ResetsAt)
		passed := 0
		for _, t := range thresholds {
			if lw.UsedPct >= float64(t) {
				passed = t
			}
		}
		if passed == 0 {
			delete(seen, key) // back under every threshold: alert again next time
			continue
		}
		if seen[key] >= passed {
			continue
		}
		seen[key] = passed
		out = append(out, limitAlert{machine: mid, agent: l.Agent, window: w.name, pct: lw.UsedPct, threshold: passed, resets: lw.ResetsAt})
	}
	return out
}

// pruneLimitAlerts forgets windows that have reset.
func pruneLimitAlerts(seen map[string]int, now time.Time) {
	for key := range seen {
		i := strings.LastIndex(key, "|")
		unix, err := strconv.ParseInt(key[i+1:], 10, 64)
		if i < 0 || err != nil || unix != 0 && time.Unix(unix, 0).Before(now) {
			delete(seen, key)
		}
	}
}

// text is the alert's message, e.g. "Claude 5-hour limit 82% used · resets
// 15:04".
func (a limitAlert) text(machineLabel string, now time.Time) string {
	s := fmt.Sprintf("%s %s limit %d%% used", shortAgent(a.agent), a.window, int(math.Floor(a.pct)))
	if !a.resets.IsZero() {
		s += " · resets " + resetText(a.resets, now)
	}
	if machineLabel != "" {
		s += " on " + machineLabel
	}
	return s
}

// alertLimits raises alerts for plan limits that just passed a threshold.
func (m *Model) alertLimits(mach *machine, l proto.PlanLimits) tea.Cmd {
	if !m.cfg.Notify.Limits {
		return nil
	}
	if m.limitSeen == nil {
		m.limitSeen = map[string]int{}
	}
	now := time.Now()
	alerts := limitCrossings(m.limitSeen, mach.id, l, m.cfg.Notify.Thresholds(), now)
	if len(alerts) == 0 {
		return nil
	}
	label := ""
	if mach.id != localMachine {
		label = mach.label
	}
	var cmds []tea.Cmd
	var texts []string
	for _, a := range alerts {
		texts = append(texts, a.text(label, now))
	}
	body := strings.Join(texts, "\n")
	m.setFlash(strings.Join(texts, " · "), alerts[len(alerts)-1].threshold >= 90)
	if !m.silenced(now) {
		cmds = append(cmds, notify(m.cfg.Notify, "conch · plan limit", body))
	}
	return tea.Batch(append(cmds, m.saveState())...)
}
