package tui

import (
	"strings"

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
	return styleWork.Render("⟳" + itoa(p.Agent.Background))
}

// backgroundSummary says what an agent left running, for a notification
// or a status line: "1 still running (npm run dev)".
func backgroundSummary(a *proto.AgentStatus) string {
	if a == nil || a.Background <= 0 {
		return ""
	}
	out := itoa(a.Background) + " still running"
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
