package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// A task where there is no repository to branch — a machine's home, a
// sandbox that holds nothing yet, a folder of notes — starts the agent in
// the folder itself with the prompt: a planning agent needs no git.

// newPlainTaskDialog asks for a prompt, the agents and how many attempts,
// and starts them in dir. dir is "" for the machine's home when its path
// isn't known; loose keeps the panes outside every project. title is the
// machine or project, where the folder, both for people.
func newPlainTaskDialog(m Model, mid, title, where, dir string, loose bool) *dialog {
	intro := "Starts the agent in " + where + " with the prompt. It is not a git repository, so there is no branch or worktree."
	d := newDialog(m, " New task · "+title+" ", []string{intro}, []string{"Prompt", "Agent", "Attempts"}, nil)
	d.fields[1].in.Placeholder = m.defaultAgent() + " (default · " + strings.Join(knownAgents(&m), ", ") + ", or several: claude,codex)"
	d.fields[2].in.Placeholder = "1 (every attempt works in the same folder)"
	defaultAgent := m.defaultAgent()
	agentsOf := func(v string) []string {
		if agents := splitAgents(v); len(agents) > 0 {
			return agents
		}
		return []string{defaultAgent}
	}
	warn := func(d *dialog) {
		agents := agentsOf(d.fields[1].in.Value())
		d.text = []string{intro}
		n, err := attemptsField(d.fields[2].in.Value())
		switch {
		case err != nil:
			d.text = append(d.text, styleErr.Render("⚠ "+err.Error()))
		case max(n, len(agents)) > 1:
			d.text = append(d.text, styleWarn.Render("⚠")+fmt.Sprintf(" %s of the same prompt, all in the same folder: nothing keeps them apart without branches.",
				counted(max(n, len(agents)), "attempt")))
		}
		for _, w := range m.limitWarnings(mid, agents, time.Now()) {
			d.text = append(d.text, styleWarn.Render("⚠")+" "+w)
		}
	}
	warn(d)
	d.onChange = warn
	d.submit = func(m *Model, v []string) tea.Cmd {
		prompt := strings.TrimSpace(v[0])
		if prompt == "" {
			return func() tea.Msg { return errMsg{errString("a task needs a prompt")} }
		}
		n, err := attemptsField(v[2])
		if err != nil {
			return func() tea.Msg { return errMsg{errString(err.Error())} }
		}
		agents := agentsOf(v[1])
		if mach := m.machine(mid); mach != nil {
			for _, agent := range agents {
				if mach.missingAgent(agent) {
					return func() tea.Msg { return askInstallMsg{machine: mid, agent: agent} }
				}
			}
		}
		if n <= 0 {
			n = len(agents)
		}
		cols, rows := m.paneArea()
		params := make([]proto.PaneCreateParams, n)
		for i := range params {
			params[i] = proto.PaneCreateParams{Agent: agents[i%len(agents)], Prompt: prompt, Cwd: dir,
				NoProject: loose, Cols: cols, Rows: rows}
		}
		if n == 1 {
			var info proto.PaneInfo
			return m.callOn(mid, proto.MethodPaneCreate, params[0], &info, func() tea.Msg { return createdMsg{machine: mid, info: info} })
		}
		m.setFlash("starting "+counted(n, "attempt")+"…", false)
		return m.startPlainAttempts(mid, params)
	}
	return d
}

// startPlainAttempts starts each agent in turn; one that fails leaves the
// others running, as a task's attempts do.
func (m Model) startPlainAttempts(mid string, params []proto.PaneCreateParams) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(mid))} }
	}
	return func() tea.Msg {
		done := attemptsDoneMsg{machine: mid, shared: true}
		for _, p := range params {
			ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
			var info proto.PaneInfo
			err := c.Call(ctx, proto.MethodPaneCreate, p, &info)
			cancel()
			if err != nil {
				done.errs = append(done.errs, p.Agent+": "+err.Error())
				continue
			}
			done.panes = append(done.panes, info)
		}
		return done
	}
}
