package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Monitoring a pane, as tmux's monitor-activity and monitor-silence: the
// server raises an alert when a watched pane prints while nobody looks at
// it (ctrl+b A), or goes quiet after printing (ctrl+b M) — a build or a
// test run finishing. Agents say for themselves when they need you; this
// is for terminals.

// toggleMonitor turns one kind of monitoring on or off for a pane: silence
// when quiet is true, activity otherwise.
func (m *Model) toggleMonitor(mid, id string, quiet bool) tea.Cmd {
	p, mach := m.pane(mid, id), m.machine(mid)
	if p == nil || mach == nil || mach.c == nil {
		return nil
	}
	if len(mach.c.MissingCapabilities([]string{proto.CapPaneMonitor})) > 0 {
		m.setFlash("conch on "+mach.label+" is too old to watch a pane; update it", true)
		return nil
	}
	var pm proto.PaneMonitor
	if p.Monitor != nil {
		pm = *p.Monitor
	}
	var what string
	switch {
	case quiet && pm.Silence > 0:
		pm.Silence, what = 0, "no alert when it goes quiet"
	case quiet:
		pm.Silence = m.cfg.Notify.SilenceAfter()
		what = fmt.Sprintf("alert when it is quiet for %ds after output", pm.Silence)
	case pm.Activity:
		pm.Activity, what = false, "no alert on output"
	default:
		pm.Activity, what = true, "alert when it prints while you are elsewhere"
	}
	m.setFlash(p.DisplayName()+": "+what, false)
	return m.callOn(mid, proto.MethodPaneMonitor, proto.PaneMonitorParams{ID: id, PaneMonitor: pm}, nil, nil)
}

// monitorItems are the pane menu's entries for monitoring.
func (m Model) monitorItems(mid, id string) []menuItem {
	p := m.pane(mid, id)
	if p == nil || p.State != proto.PaneRunning {
		return nil
	}
	quiet, output := "Alert when it goes quiet", "Alert on output"
	if p.Monitor != nil && p.Monitor.Silence > 0 {
		quiet = "✓ " + quiet
	}
	if p.Monitor != nil && p.Monitor.Activity {
		output = "✓ " + output
	}
	return []menuItem{
		{"", quiet + " (" + m.cfg.Keys.Prefix + " M)", func(m *Model) tea.Cmd { return m.toggleMonitor(mid, id, true) }},
		{"", output + " (" + m.cfg.Keys.Prefix + " A)", func(m *Model) tea.Cmd { return m.toggleMonitor(mid, id, false) }},
	}
}

// alertBody says what a monitored pane did.
func alertBody(info proto.PaneInfo) string {
	if info.Alert == proto.AlertSilence && info.Monitor != nil {
		return fmt.Sprintf("%s has been quiet for %ds", info.DisplayName(), info.Monitor.Silence)
	}
	if info.Alert == proto.AlertSilence {
		return info.DisplayName() + " has gone quiet"
	}
	return info.DisplayName() + " printed something"
}

// notifyMonitor tells the user about an alert a monitored pane just raised.
func (m Model) notifyMonitor(mach *machine, old, info proto.PaneInfo) tea.Cmd {
	if info.Alert == "" || info.Alert == old.Alert || m.isViewing(mach.id, info.ID) || m.silenced(time.Now()) {
		return nil
	}
	title := "conch · terminal"
	if info.Agent != nil {
		title = "conch · " + info.Agent.Name
	}
	if mach.id != localMachine {
		title += " on " + mach.label
	}
	return notify(m.cfg.Notify, title, alertBody(info))
}
