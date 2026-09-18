package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Best-of-N: the same prompt tried several times, each attempt on its own
// branch and worktree, so the results can be compared and one kept.

// maxAttempts caps how many agents one task starts: each burns its own plan
// window.
const maxAttempts = 10

// attempt is one try at a prompt.
type attempt struct{ agent, branch string }

// attemptsDoneMsg reports what starting a task's attempts did.
type attemptsDoneMsg struct {
	machine string
	panes   []proto.PaneInfo
	errs    []string
}

// attemptPlan names the attempts: one per agent by default, n of them when
// asked, cycling through the agents. One attempt keeps the plain branch
// name, so the usual case is unchanged.
func attemptPlan(agents []string, n int, branch, prompt string, existing []proto.BranchInfo) []attempt {
	if len(agents) == 0 {
		agents = []string{""}
	}
	if n <= 0 {
		n = len(agents)
	}
	base := branch
	if base == "" {
		base = gitx.BranchFromPrompt(prompt)
	}
	if n == 1 {
		return []attempt{{agent: agents[0], branch: branch}}
	}
	taken := map[string]bool{}
	for _, b := range existing {
		taken[b.Name] = true
	}
	counts := map[string]int{}
	out := make([]attempt, 0, n)
	for i := 0; i < n; i++ {
		agent := agents[i%len(agents)]
		counts[agent]++
		name := gitx.AttemptBranch(base, agent, counts[agent], func(s string) bool { return taken[s] })
		taken[name] = true
		out = append(out, attempt{agent: agent, branch: name})
	}
	return out
}

// splitAgents parses an Agent field: "claude, codex" names two agents.
func splitAgents(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// startAttempts creates each attempt in turn. One that fails (a branch
// already taken, an agent that won't start) leaves the others running.
func (m Model) startAttempts(mid, projectID, prompt, base string, attempts []attempt, cols, rows int) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(mid))} }
	}
	return func() tea.Msg {
		done := attemptsDoneMsg{machine: mid}
		for _, at := range attempts {
			ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
			var info proto.PaneInfo
			params := proto.TaskCreateParams{ProjectID: projectID, Prompt: prompt, Branch: at.branch,
				Base: base, Agent: at.agent, Cols: cols, Rows: rows}
			err := c.Call(ctx, proto.MethodTaskCreate, params, &info)
			cancel()
			if err != nil {
				name := at.branch
				if name == "" {
					name = at.agent
				}
				done.errs = append(done.errs, name+": "+err.Error())
				continue
			}
			done.panes = append(done.panes, info)
		}
		return done
	}
}

// receiveAttempts shows the first attempt and says how the rest went.
func (m *Model) receiveAttempts(msg attemptsDoneMsg) tea.Cmd {
	mach := m.machine(msg.machine)
	for _, info := range msg.panes {
		if mach != nil && mach.paneIndex(info.ID) < 0 {
			mach.panes = append(mach.panes, info)
		}
	}
	switch {
	case len(msg.panes) == 0:
		m.setFlash(strings.Join(msg.errs, "; "), true)
		return m.rebuild()
	case len(msg.errs) > 0:
		m.setFlash(fmt.Sprintf("started %s; %s", counted(len(msg.panes), "attempt"), strings.Join(msg.errs, "; ")), true)
	case len(msg.panes) > 1:
		m.setFlash("started "+counted(len(msg.panes), "attempts")+" on their own branches", false)
	}
	m.revealPane(msg.machine, msg.panes[0])
	m.focus = focusMain
	return tea.Batch(m.rebuild(), m.saveState())
}

// attemptsField reads the Attempts field: empty or 1 is a plain task.
func attemptsField(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	switch {
	case err != nil:
		return 0, fmt.Errorf("attempts: %q is not a number", s)
	case n < 1:
		return 0, fmt.Errorf("attempts: %d is fewer than one", n)
	case n > maxAttempts:
		return 0, fmt.Errorf("attempts: %d is more than %d", n, maxAttempts)
	}
	return n, nil
}

// limitWarnings is the plan-limit warning for each agent an attempt uses,
// so starting five Claudes says so once.
func (m Model) limitWarnings(mid string, agents []string, now time.Time) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range agents {
		if seen[a] {
			continue
		}
		seen[a] = true
		if w := m.limitWarning(mid, a, now); w != "" {
			out = append(out, w)
		}
	}
	return out
}
