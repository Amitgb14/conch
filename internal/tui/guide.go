package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// The first five minutes. conch does a great deal, and its first screen
// used to list keys without saying what they were for: nothing told a
// newcomer that the loop is a project, an agent given a task on a branch
// of its own, the queue to see what came back, and quitting without
// stopping anybody. The guide is that loop as four steps on the local
// machine's page, each ticking itself off as it is done — never a step
// done for show — and gone once all four are, or when x hides it.
//
// What has happened is kept in ui.json (uiState.Guide), shared between
// TUIs like the folders are, since an agent started in one window is a
// step done in the other too.

// Guide steps, as kept in ui.json. guideHidden is x on the page.
const (
	guideTask     = "task"
	guideQueue    = "queue"
	guideReturned = "returned"
	guideHidden   = "hidden"
)

// guideStep is one step of the guide.
type guideStep struct {
	key, does string
	done      bool
}

// guideSteps are the guide's steps as they stand. Adding a project is
// read off the machines rather than kept: a project is still there, or it
// is not.
func (m Model) guideSteps() []guideStep {
	projects := false
	for _, mach := range m.machines {
		if len(mach.projects) > 0 {
			projects = true
		}
	}
	return []guideStep{
		{"a", "add a project: a git repository you work in", projects},
		{"t", "on a project, give an agent a task: a branch and worktree of its own", m.guide[guideTask]},
		{"Q", "the review queue: who is waiting for you, and work ready to land", m.guide[guideQueue]},
		{"q", "detach whenever you like: agents keep running, and conch finds them again", m.guide[guideReturned]},
	}
}

// guideShown is whether the guide is on the local machine's page.
func (m Model) guideShown() bool {
	if m.guide[guideHidden] {
		return false
	}
	for _, s := range m.guideSteps() {
		if !s.done {
			return true
		}
	}
	return false
}

// guideLines is the guide on the local machine's page, or nothing once
// it has gone.
func (m Model) guideLines() []string {
	if !m.guideShown() {
		return nil
	}
	steps := m.guideSteps()
	done := 0
	for _, s := range steps {
		if s.done {
			done++
		}
	}
	lines := []string{"", styleBold.Render("Getting started") +
		styleMuted.Render(fmt.Sprintf("  %d of %d · x hides this", done, len(steps)))}
	for _, s := range steps {
		if s.done {
			lines = append(lines, styleOK.Render("✓ ")+styleMuted.Render(s.key+"  "+s.does))
			continue
		}
		lines = append(lines, "  "+styleBold.Render(s.key)+"  "+s.does)
	}
	return lines
}

// guideNote records that a step happened, saving it once: a step is
// noted at most once, so this is cheap to call wherever it may have.
func (m *Model) guideNote(step string) tea.Cmd {
	if m.guide[step] {
		return nil
	}
	if m.guide == nil {
		m.guide = map[string]bool{}
	}
	m.guide[step] = true
	return m.saveState()
}

// guideAgents notes the steps a machine's panes show: an agent working in
// a project is a task given, and an agent already running when this TUI
// first heard from the machine is one that outlived the TUI before it.
func (m *Model) guideAgents(mach *machine, first bool) tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range mach.panes {
		if p.Agent == nil {
			continue
		}
		if p.ProjectID != "" {
			cmds = append(cmds, m.guideNote(guideTask))
		}
		if first {
			cmds = append(cmds, m.guideNote(guideReturned))
		}
	}
	return tea.Batch(cmds...)
}
