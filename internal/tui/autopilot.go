package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// A server with the autopilot (CapAutopilot) sends a branch's failures
// back to its agent and merges a green pull request by itself, with or
// without a TUI, so this side only hands it the policy — at every
// connection and whenever Settings changes it — tells it how a check this
// TUI ran ended, and says what it reports. Against an older server the TUI
// still does both itself (verifyfix.go), and only while it runs.

// autopilotOn says whether the server on machine minds its branches
// itself.
func (m Model) autopilotOn(machine string) bool {
	return m.hasCapability(machine, proto.CapAutopilot)
}

// autopilotPolicy is the [verify] settings the servers act on.
func (m Model) autopilotPolicy() proto.AutopilotPolicy {
	pol := proto.AutopilotPolicy{AutoFix: m.cfg.Verify.AutoFix, AutoMerge: m.cfg.Verify.AutoMerge, AutoPR: m.cfg.Verify.AutoPR, AutoResolve: m.cfg.Verify.AutoResolve}
	for id, cmd := range m.cfg.Verify.Commands {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			if pol.Checks == nil {
				pol.Checks = map[string]string{}
			}
			pol.Checks[id] = cmd
		}
	}
	return pol
}

// pushAutopilot hands machine's server the policy.
func (m *Model) pushAutopilot(machine string) tea.Cmd {
	c := m.clientOf(machine)
	if c == nil || !m.autopilotOn(machine) {
		return nil
	}
	pol := m.autopilotPolicy()
	return func() tea.Msg {
		if err := callCtx(c, proto.MethodAutopilotSet, pol, nil); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// pushAutopilotAll hands every connected server the policy, after a
// change in Settings.
func (m *Model) pushAutopilotAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		cmds = append(cmds, m.pushAutopilot(mach.id))
	}
	return tea.Batch(cmds...)
}

// reportCheck tells the server how a check ended: a failure holds the
// branch's pull request back from merging and, within auto_fix, goes to
// its agent; a pass lifts the hold.
func (m *Model) reportCheck(machine, key string, run verifyRun) tea.Cmd {
	c := m.clientOf(machine)
	parts := strings.SplitN(key, "|", 3)
	if c == nil || len(parts) != 3 {
		return nil
	}
	ck := proto.AutopilotCheck{ProjectID: parts[1], Branch: parts[2], Pane: run.pane, Command: m.verifyCommand(parts[1]), Exit: run.exit}
	return func() tea.Msg {
		if err := callCtx(c, proto.MethodAutopilotCheck, ck, nil); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// autopilotNote is what the server did, or held back from, on its own.
func (m *Model) autopilotNote(machine string, n proto.AutopilotNote) tea.Cmd {
	if !n.Merged {
		m.setFlash(n.Text, n.Error)
		return nil
	}
	return m.receiveHarvest(harvestDoneMsg{target: harvestTarget{machine: machine, projectID: n.ProjectID, branch: n.Branch}, text: n.Text})
}

// adoptCheck takes a check the server started on a branch for the
// review queue's own, so it shows as checking, passed or failed the way
// one started here does — but is not reported back to the server, which
// read it itself.
func (m *Model) adoptCheck(machine string, p proto.PaneInfo) {
	if !strings.HasPrefix(p.Name, "check · ") || p.ProjectID == "" || p.Branch == "" || !m.autopilotOn(machine) {
		return
	}
	key := verifyKey(machine, p.ProjectID, p.Branch)
	if run, ok := m.verifyRuns[key]; ok && (run.pane == p.ID || run.pane == "" && !run.done && run.err == "") {
		return // ours, or ours on its way
	}
	if m.verifyRuns == nil {
		m.verifyRuns = map[string]verifyRun{}
	}
	run := verifyRun{pane: p.ID, server: true, sig: m.branchSig(machine, p.ProjectID, p.Branch)}
	if p.State == proto.PaneExited {
		run.done, run.exit = true, p.ExitCode
	}
	m.verifyRuns[key] = run
}
