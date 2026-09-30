package tui

import (
	"github.com/Amitgb14/conch/internal/proto"
)

// Who started what. A pane an agent started carries the pane it came from
// (CreatedBy, capability pane.scope.v1), so a reviewer reads as the
// reviewer of something rather than as one more agent in the list, and the
// agent that asked for it can say how many are working for it.

// creatorOf is the pane that started this one, if it is still there.
func (m Model) creatorOf(mid string, p proto.PaneInfo) *proto.PaneInfo {
	if p.CreatedBy == "" {
		return nil
	}
	return m.pane(mid, p.CreatedBy)
}

// forWhom names the agent a pane is working for, as " for reviewer", or ""
// when nobody started it. It is the same words in the tree, in the queue
// and in a notification, so the three read as one thing.
func (m Model) forWhom(mid string, p proto.PaneInfo) string {
	c := m.creatorOf(mid, p)
	if c == nil {
		return ""
	}
	return " for " + c.DisplayName()
}

// helpersOf counts the panes an agent started that are still running, and
// how many of those are waiting on a question.
func (m Model) helpersOf(mid, paneID string) (total, waiting int) {
	mach := m.machine(mid)
	if mach == nil || paneID == "" {
		return 0, 0
	}
	for _, p := range mach.panes {
		if p.CreatedBy != paneID || p.State != proto.PaneRunning {
			continue
		}
		total++
		if p.Agent != nil && p.Agent.State == proto.AgentBlocked {
			waiting++
		}
	}
	return total, waiting
}

// helperChip is what a creator's row says about the panes it started: how
// many, and in the warn colour when one of them is waiting for an answer,
// since that is the one that needs somebody.
func (m Model) helperChip(mid, paneID string) string {
	total, waiting := m.helpersOf(mid, paneID)
	if total == 0 {
		return ""
	}
	text := "⑂" + itoa(total)
	if waiting > 0 {
		return styleWarn.Render(text + "!")
	}
	return styleMuted.Render(text)
}
