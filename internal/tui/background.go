package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amitgb14/conch/internal/proto"
)

// What an agent left running when it stopped working (agent.background.v1):
// a dev server, a watcher, a background shell. Without a mark an agent in
// that state looks finished and quiet, when it can still be changing files
// or holding a port.

// backgroundChip is the tree's mark for an agent's background jobs, ⟳ and
// how many, or "" for none.
func backgroundChip(p proto.PaneInfo) string {
	if p.State != proto.PaneRunning || p.Agent == nil || p.Agent.Background <= 0 {
		return ""
	}
	return styleWork.Render("⟳" + strconv.Itoa(p.Agent.Background))
}

// backgroundSummary says what an agent left running, for a notification
// or a status line: "1 still running (npm run dev)".
func backgroundSummary(a *proto.AgentStatus) string {
	if a == nil || a.Background <= 0 {
		return ""
	}
	out := strconv.Itoa(a.Background) + " still running"
	var names []string
	for i, j := range a.Jobs {
		if i == 2 {
			names = append(names, "…")
			break
		}
		cmd := j.Command
		if r := []rune(cmd); len(r) > 40 {
			cmd = string(r[:39]) + "…"
		}
		names = append(names, cmd)
	}
	if len(names) > 0 {
		out += " (" + strings.Join(names, ", ") + ")"
	}
	return out
}

// jobOf is the job a kindJob row is, if its agent still lists it.
func (m Model) jobOf(r row) (proto.BackgroundJob, bool) {
	p := m.pane(r.machine, r.paneID)
	if p == nil || p.Agent == nil {
		return proto.BackgroundJob{}, false
	}
	for _, j := range p.Agent.Jobs {
		if strconv.Itoa(j.PID) == r.label {
			return j, true
		}
	}
	return proto.BackgroundJob{}, false
}

// jobParts draws a job under its agent: the command, then the ports it
// listens on and how long it has run.
func (m Model) jobParts(r row) (string, lipgloss.Style, string, lipgloss.Style, string) {
	style := styleWork
	if mach := m.machine(r.machine); mach != nil && mach.state != stateOnline {
		style = styleMuted // last seen; not live
	}
	j, ok := m.jobOf(r)
	if !ok {
		return "⟳", styleMuted, "ended", styleMuted, ""
	}
	var parts []string
	for _, p := range j.Ports {
		parts = append(parts, ":"+strconv.Itoa(p))
	}
	if !j.Since.IsZero() {
		parts = append(parts, jobAge(time.Since(j.Since)))
	}
	return "⟳", style, j.Command, lipgloss.NewStyle(), styleMuted.Render(strings.Join(parts, " "))
}

// jobAge is how long a job has run, to the second while that is short.
func jobAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	}
	return shortDuration(d)
}

// stopJob asks the pane's server to end a job its agent left running.
func (m *Model) stopJob(mid, paneID string, pid int) tea.Cmd {
	if m.machine(mid) == nil {
		return nil
	}
	if !m.hasCapability(mid, proto.CapAgentBackground) {
		m.setFlash("the server there is too old to stop background jobs; reload it", true)
		return nil
	}
	what := "pid " + strconv.Itoa(pid)
	if j, ok := m.jobOf(row{machine: mid, paneID: paneID, label: strconv.Itoa(pid)}); ok && j.Command != "" {
		what = j.Command
	}
	m.setFlash("stopping "+what, false)
	return m.callOn(mid, proto.MethodAgentBackgroundStop, proto.AgentBackgroundStopParams{ID: paneID, PID: pid}, nil, nil)
}

// atoiOr is s as a number, or 0.
func atoiOr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
