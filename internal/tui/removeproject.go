package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Removing a project from conch leaves the panes working in it running,
// in no project, unless the person asks for them to be closed with it:
// agents are never ended without being asked. Asked once, here, with
// the count, so nobody finds them still running later (or the project
// back after a reload, from one of their folders).

// projectPanes counts the running panes in a project: agents, and the
// rest.
func (m Model) projectPanes(mid, projectID string) (agents, others int) {
	mach := m.machine(mid)
	if mach == nil {
		return 0, 0
	}
	for _, p := range mach.panes {
		if p.ProjectID != projectID || p.State != proto.PaneRunning || m.isOwnPane(mid, p.ID) {
			continue
		}
		if p.Agent != nil {
			agents++
		} else {
			others++
		}
	}
	return agents, others
}

// panesPhrase is "2 agents and 1 terminal".
func panesPhrase(agents, others int) string {
	switch {
	case agents > 0 && others > 0:
		return countOf(agents, "agent") + " and " + countOf(others, "terminal")
	case agents > 0:
		return countOf(agents, "agent")
	}
	return countOf(others, "terminal")
}

// confirmRemoveProject asks before conch forgets a project: plainly when
// nothing runs in it, else whether its panes stay or close with it.
func (m *Model) confirmRemoveProject(mid string, proj proto.ProjectInfo) {
	id, name := proj.ID, proj.Name
	remove := func(close bool) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			var res proto.ProjectRemoveResult
			return m.callOn(mid, proto.MethodProjectRemove, proto.ProjectRemoveParams{ID: id, Close: close}, &res, func() tea.Msg {
				if len(res.Closed) > 0 {
					return flashMsg(fmt.Sprintf("removed %s and closed %s", name, countOf(len(res.Closed), "pane")))
				}
				return nil
			})
		}
	}
	agents, others := m.projectPanes(mid, id)
	if agents+others == 0 {
		m.overlay = newConfirm(fmt.Sprintf("Remove %s from the sidebar? Files are not touched.", name), remove(false))
		return
	}
	running := panesPhrase(agents, others)
	if !m.hasCapability(mid, proto.CapProjectRemoveClose) {
		// An older server cannot close them with it: say they stay.
		m.overlay = newConfirm(fmt.Sprintf("Remove %s from the sidebar? Files are not touched; its %s keep running.", name, running), remove(false))
		return
	}
	m.overlay = &menu{
		title: "Remove " + name + " from the sidebar?",
		items: []menuItem{
			{"r", "Remove; its " + running + " keep running", remove(false)},
			{"c", "Remove and close its " + running, remove(true)},
			{"", styleMuted.Render("Files, branches and worktrees are not touched."), nil},
			{"esc", "Cancel", func(*Model) tea.Cmd { return nil }},
		},
		x: max(m.width/2-30, 0), y: max(m.height/3, 0),
	}
}
