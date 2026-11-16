package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// A project's integrations (mock 5, Conch.app's sheet of the same name):
// one line per kind — what it is hosted on, which CI and tracker it uses,
// the sandboxes set up — with each one's state and what to do about it,
// from the server's integration.detect; and the repository's
// .conch/integrations.toml, asked about once and saved for the team.

type projectIntegrationsMsg struct {
	machine, projectID string
	detect             proto.IntegrationDetect
	err                error
	// answered: the team file's question was just answered (or the file
	// written), so it is not asked again on the way back.
	answered bool
}

// openProjectIntegrations asks the project's server what it uses.
func (m *Model) openProjectIntegrations(machine, projectID string) tea.Cmd {
	proj := m.project(machine, projectID)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("integrations belong to a git project", true)
		return nil
	case !m.hasCapability(machine, proto.CapIntegrationDetect):
		m.setFlash("the server there predates integrations; reload it (conch server reload)", true)
		return nil
	}
	c := m.clientOf(machine)
	return func() tea.Msg {
		var d proto.IntegrationDetect
		err := callCtx(c, proto.MethodIntegrationDetect, proto.IntegrationDetectParams{ProjectID: projectID}, &d)
		return projectIntegrationsMsg{machine: machine, projectID: projectID, detect: d, err: err}
	}
}

// projectTeam answers the team file, or writes or removes it, and comes
// back with the project's integrations.
func (m *Model) projectTeam(machine, projectID, action string, names []string) tea.Cmd {
	c := m.clientOf(machine)
	if c == nil {
		m.setFlash(m.offlineText(machine), true)
		return nil
	}
	return func() tea.Msg {
		var d proto.IntegrationDetect
		err := callCtx(c, proto.MethodIntegrationTeam, proto.IntegrationTeamParams{ProjectID: projectID, Action: action, Names: names}, &d)
		return projectIntegrationsMsg{machine: machine, projectID: projectID, detect: d, err: err, answered: true}
	}
}

// receiveProjectIntegrations asks the team file's question first, when
// there is one to ask, and otherwise shows the project's integrations as a
// menu whose lines do what each needs.
func (m *Model) receiveProjectIntegrations(msg projectIntegrationsMsg) tea.Cmd {
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	mid, pid := msg.machine, msg.projectID
	team := m.hasCapability(mid, proto.CapIntegrationTeam)
	if q := teamQuestion(msg.detect.Team); q != "" && team && !msg.answered {
		d := newConfirm(q, func(m *Model) tea.Cmd { return m.projectTeam(mid, pid, proto.TeamUse, nil) })
		d.decline = func(m *Model) tea.Cmd { return m.projectTeam(mid, pid, proto.TeamIgnore, nil) }
		m.overlay = d
		return nil
	}
	m.overlay = &menu{title: m.projectName(mid, pid) + " — Integrations", items: m.projectIntegrationItems(mid, pid, msg.detect, team)}
	return nil
}

// teamQuestion is the question a team file waiting for an answer asks.
func teamQuestion(t *proto.IntegrationTeam) string {
	if t == nil || t.State != "ask" || t.Problem != "" || len(t.Names) == 0 {
		return ""
	}
	labels := make([]string, len(t.Names))
	for i, n := range t.Names {
		labels[i] = integration.Label(n, integration.Cached())
	}
	list := labels[0]
	if len(labels) > 1 {
		list = strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
	}
	them := "them"
	if len(labels) == 1 {
		them = "it"
	}
	return "This repository's " + t.Path + " says it uses " + list + ". Use " + them + "?"
}

var projectKinds = []struct{ kind, title, none string }{
	{integration.KindHost, "Code host", "no remote conch knows"},
	{integration.KindCI, "CI", "CircleCI, GitLab CI, Buildkite, Jenkins…"},
	{integration.KindTickets, "Tickets", "Linear, Jira, GitHub Issues…"},
	{integration.KindSandbox, "Sandboxes", "Daytona, E2B, boat.dev…"},
	{integration.KindNotify, "Notifications", "Slack, Discord, ntfy…"},
}

// useChip is a service's state in a word: the sheet's chip.
func useChip(u proto.IntegrationUse) string {
	switch {
	case u.Kind == integration.KindSandbox:
		return "set up"
	case !u.Installed:
		return "not installed"
	case u.Connected:
		return "connected"
	}
	return "key not set"
}

// projectIntegrationItems are the menu's lines: a kind each, then what to
// do for each service that needs something, then the team file.
func (m *Model) projectIntegrationItems(mid, pid string, d proto.IntegrationDetect, team bool) []menuItem {
	var items []menuItem
	here := mid == localMachine // a key is given where the server runs
	for _, k := range projectKinds {
		var names, chips []string
		var acts []menuItem
		for _, u := range d.Integrations {
			if u.Kind != k.kind {
				continue
			}
			names = append(names, u.Label)
			chip := useChip(u)
			chips = append(chips, chip)
			name, label := u.Name, u.Label
			switch {
			case !here:
			case chip == "key not set":
				acts = append(acts, menuItem{"", "    Connect " + label + "…", func(m *Model) tea.Cmd {
					return conchInPane(m, "integration key", "giving "+name+" its key in this terminal", integrationScript("key", name))
				}})
			case chip == "not installed":
				acts = append(acts, menuItem{"", "    Install " + label + "…", func(m *Model) tea.Cmd {
					return conchInPane(m, "install an integration", "installing "+name, integrationInstallScript(name))
				}})
			}
		}
		if len(names) == 0 {
			tab := integrationsTab
			if k.kind == integration.KindSandbox {
				tab = sandboxTab
			}
			items = append(items, menuItem{"", k.title + " · none — " + k.none, func(m *Model) tea.Cmd {
				s, cmd := newSettings(m)
				s.setTab(tab)
				m.overlay = s
				return cmd
			}})
			continue
		}
		items = append(items, menuItem{"", k.title + " · " + strings.Join(names, ", ") + "  " + strings.Join(chips, ", "), func(*Model) tea.Cmd { return nil }})
		items = append(items, acts...)
	}
	if !team {
		return items
	}
	t := d.Team
	switch {
	case t != nil && t.Problem != "":
		items = append(items, menuItem{"", integration.TeamFile + ": " + t.Problem, func(*Model) tea.Cmd { return nil }})
	case t != nil && t.State == "used":
		items = append(items, menuItem{"s", "☑ Saved in " + integration.TeamFile + " for the team (remove it)", func(m *Model) tea.Cmd {
			return m.projectTeam(mid, pid, proto.TeamRemove, nil)
		}})
		return items
	}
	names := teamNames(d)
	if len(names) > 0 {
		items = append(items, menuItem{"s", "☐ Save in " + integration.TeamFile + " for the team", func(m *Model) tea.Cmd {
			return m.projectTeam(mid, pid, proto.TeamSave, names)
		}})
	}
	return items
}

// teamNames is what saving for the team writes: the names of what the
// project uses that something here reads. A sandbox is this computer's,
// not the repository's.
func teamNames(d proto.IntegrationDetect) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range d.Integrations {
		if u.Installed && u.Kind != integration.KindSandbox && !seen[u.Name] {
			seen[u.Name] = true
			out = append(out, u.Name)
		}
	}
	return out
}
