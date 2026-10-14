package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// A task from a GitHub issue: the project's open issues, read through gh on
// its machine, and choosing one starts the default agent on a branch named
// after it, given the issue as its prompt.

type issuesMsg struct {
	machine, projectID string
	list               proto.IssueList
	err                error
}

// openIssues reads a project's open issues.
func (m *Model) openIssues(machine, projectID string) tea.Cmd {
	proj := m.project(machine, projectID)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("issues belong to a git project with a GitHub remote", true)
		return nil
	case !m.hasCapability(machine, proto.CapIssues):
		m.setFlash("the server there predates tasks from issues; reload it (conch server reload)", true)
		return nil
	}
	c := m.clientOf(machine)
	m.setFlash("reading "+proj.Name+"'s issues…", false)
	return func() tea.Msg {
		var list proto.IssueList
		err := callCtx(c, proto.MethodProjectIssues, proto.IssuesParams{ProjectID: projectID, Limit: 30}, &list)
		return issuesMsg{machine: machine, projectID: projectID, list: list, err: err}
	}
}

// receiveIssues offers the issues as a menu.
func (m *Model) receiveIssues(msg issuesMsg) tea.Cmd {
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	name := m.projectName(msg.machine, msg.projectID)
	if len(msg.list.Issues) == 0 {
		m.setFlash(name+" has no open issues", false)
		return nil
	}
	m.setFlash("", false)
	var items []menuItem
	for i, is := range msg.list.Issues {
		key := ""
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		label := fmt.Sprintf("#%d %s", is.Number, is.Title)
		if len(is.Labels) > 0 {
			label += "  [" + strings.Join(is.Labels, ", ") + "]"
		}
		number := is.Number
		mid, pid := msg.machine, msg.projectID
		items = append(items, menuItem{key, ansi.Truncate(label, 70, "…"), func(m *Model) tea.Cmd {
			return m.startIssueTask(mid, pid, number)
		}})
	}
	m.overlay = &menu{title: "Start a task from an issue of " + name, items: items,
		x: max(m.width/2-36, 0), y: max(m.height/4, 0)}
	return nil
}

// startIssueTask starts the default agent on an issue.
func (m *Model) startIssueTask(machine, projectID string, number int) tea.Cmd {
	agent := m.defaultAgent()
	if mach := m.machine(machine); mach != nil && mach.missingAgent(agent) {
		return func() tea.Msg { return askInstallMsg{machine: machine, agent: agent} }
	}
	cols, rows := m.paneArea()
	params := proto.TaskCreateParams{ProjectID: projectID, Issue: number, Agent: agent, Cols: cols, Rows: rows}
	m.setFlash(fmt.Sprintf("starting %s on issue #%d…", agentLabel(agent), number), false)
	var info proto.PaneInfo
	return m.callOn(machine, proto.MethodTaskCreate, params, &info, func() tea.Msg { return createdMsg{machine: machine, info: info} })
}
