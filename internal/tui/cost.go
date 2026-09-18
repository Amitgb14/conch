package tui

import (
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// What agents are spending, shown in the tree beside what they are doing.
// Only the usage conch has seen is counted: an agent started outside conch
// spends the same plan window without appearing here, so totals are labelled
// rather than passed off as the window's.

// usage is what one or more agents have used so far.
type usage struct {
	agents  int
	output  int // tokens generated
	input   int // prompt tokens, cache reads and writes included
	cost    float64
	costFor int // how many of the agents reported a cost
}

func (u usage) empty() bool { return u.agents == 0 || (u.output == 0 && u.input == 0 && u.cost == 0) }

// reportsCost says whether every agent counted reported a cost, so a "$"
// chip stands for all of them rather than some.
func (u usage) reportsCost() bool { return u.cost > 0 && u.costFor == u.agents }

// add counts a pane's usage, if it is an agent that reports any.
func (u *usage) add(p proto.PaneInfo) {
	if p.Agent == nil || p.Agent.Tokens == nil {
		return
	}
	t := p.Agent.Tokens
	u.agents++
	u.output += t.Output
	u.input += t.Input + t.CacheRead + t.CacheWrite
	if t.CostUSD > 0 {
		u.cost += t.CostUSD
		u.costFor++
	}
}

// chip is the short form for a tree row: what it cost when the agent
// reports one (only Claude Code and OpenCode do), else what it has used.
// An agent that has read a large context but written nothing yet still
// shows what it spent getting there.
func (u usage) chip() string {
	switch {
	case u.empty():
		return ""
	case u.cost > 0:
		return usd(u.cost)
	case u.output > 0:
		return humanCount(u.output) + " out"
	case u.input > 0:
		return humanCount(u.input) + " in"
	}
	return ""
}

// line is the full form for a project or machine page, saying plainly that
// this is only what conch has seen.
func (u usage) line(what string) string {
	if u.empty() {
		return ""
	}
	parts := []string{fmt.Sprintf("%s conch has seen from %s", what, counted(u.agents, "agent"))}
	if u.input > 0 || u.output > 0 {
		parts = append(parts, "in "+humanCount(u.input), "out "+humanCount(u.output))
	}
	if u.cost > 0 {
		cost := usd(u.cost) + " reported"
		if u.costFor < u.agents {
			cost = fmt.Sprintf("%s reported by %d of them", usd(u.cost), u.costFor)
		}
		parts = append(parts, cost)
	}
	return strings.Join(parts, " · ")
}

// paneUsage is one pane's usage.
func paneUsage(p proto.PaneInfo) usage {
	var u usage
	u.add(p)
	return u
}

// projectUsage sums the agents of a project on a machine, and sectionUsage
// those of one of its sections.
func (m Model) projectUsage(mid, projectID string) usage {
	var u usage
	mach := m.machine(mid)
	if mach == nil {
		return u
	}
	for _, p := range mach.panes {
		if p.ProjectID == projectID {
			u.add(p) // only agents with usage count; add ignores the rest
		}
	}
	return u
}

// looseUsage sums the agents that belong to no project (the CLI group).
func (m Model) looseUsage(mid string) usage {
	var u usage
	mach := m.machine(mid)
	if mach == nil {
		return u
	}
	for _, p := range mach.panes {
		if p.ProjectID == "" {
			u.add(p)
		}
	}
	return u
}

// usageOf sums the agents among panes.
func usageOf(panes []proto.PaneInfo) usage {
	var u usage
	for _, p := range panes {
		u.add(p)
	}
	return u
}

// machineUsage sums every agent on a machine.
func (m Model) machineUsage(mid string) usage {
	if mach := m.machine(mid); mach != nil {
		return usageOf(mach.panes)
	}
	return usage{}
}

// branchUsage sums the agents working on one branch.
func (m Model) branchUsage(mid, projectID, branch string) usage {
	var u usage
	mach := m.machine(mid)
	if mach == nil {
		return u
	}
	for _, p := range mach.panes {
		if p.ProjectID == projectID && p.Branch == branch {
			u.add(p)
		}
	}
	return u
}

// costChip is a tree row's usage detail, styled and muted so it never
// competes with what an agent is doing. Settings → Theme → Tree turns it
// off, and then nothing anywhere shows it.
func (m Model) costChip(u usage) string {
	chip := u.chip()
	if chip == "" || !m.cfg.UI.Cost {
		return ""
	}
	return styleMuted.Render(chip)
}

// usageLine is line() for the project and machine pages, subject to the
// same setting.
func (m Model) usageLine(u usage, what string) string {
	if !m.cfg.UI.Cost {
		return ""
	}
	return u.line(what)
}

// sessionUsage is what a saved conversation used, as its store recorded it.
// Claude Code and OpenCode keep a cost, Codex the tokens it generated, and
// Gemini CLI neither: then there is nothing to show.
func sessionUsage(s proto.SessionInfo) usage {
	u := usage{agents: 1, output: s.Output, cost: s.CostUSD}
	if s.CostUSD > 0 {
		u.costFor = 1
	}
	return u
}
