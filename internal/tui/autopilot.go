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
	return proto.AutopilotPolicy{AutoFix: m.cfg.Verify.AutoFix, AutoMerge: m.cfg.Verify.AutoMerge}
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
