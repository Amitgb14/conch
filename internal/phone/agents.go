package phone

import (
	"context"
	"sort"

	"github.com/Amitgb14/conch/internal/proto"
)

// phoneState is an agent's state in the phone's words.
func phoneState(s string) string {
	switch s {
	case proto.AgentBlocked:
		return StateWaiting
	case proto.AgentWorking:
		return StateWorking
	case proto.AgentDone:
		return StateDone
	}
	return StateIdle
}

// isAgent reports whether a pane is one the list carries: running, with an
// agent detected in it.
func isAgent(p proto.PaneInfo) bool {
	return p.State == proto.PaneRunning && p.Agent != nil
}

// buildAgent is a pane as the phone sees it. projects names the projects
// by ID; screen is the pane's plain screen, needed only while it waits.
func buildAgent(machine string, p proto.PaneInfo, projects map[string]string, screen []string) Agent {
	a := Agent{
		Machine: machine, Pane: composePaneID(machine, p.ID), Name: p.Name, Branch: p.Branch,
		Agent: p.Agent.Name, State: phoneState(p.Agent.State), Since: p.Agent.Since.UTC(),
		Title: p.Title, CreatedBy: p.CreatedBy, Failed: p.Agent.Failed,
		Question: readQuestion(p, screen),
	}
	if p.ProjectID != "" {
		a.Project = &ProjectRef{ID: p.ProjectID, Name: projects[p.ProjectID]}
	}
	if p.Agent.Tokens != nil {
		a.CostUSD = p.Agent.Tokens.CostUSD
	}
	return a
}

// projectNames asks the server what its projects are called.
func projectNames(ctx context.Context, c caller) (map[string]string, error) {
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil {
		return nil, err
	}
	names := make(map[string]string, len(list.Projects))
	for _, p := range list.Projects {
		names[p.ID] = p.Name
	}
	return names, nil
}

// agentOf builds one pane's agent, reading its screen when it waits. A
// screen that can't be read — the pane went as we asked — leaves the
// question without choices rather than failing the list.
func agentOf(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string) Agent {
	var screen proto.PaneReadResult
	if p.Agent.State == proto.AgentBlocked {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: p.ID}, &screen)
	}
	return buildAgent(machine, p, projects, screen.Lines)
}

// agentList is every agent, waiting first and longest wait first, as the
// TUI's review queue orders them.
func agentList(ctx context.Context, machine string, c caller) ([]Agent, error) {
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		return nil, err
	}
	projects, err := projectNames(ctx, c)
	if err != nil {
		return nil, err
	}
	agents := []Agent{}
	for _, p := range list.Panes {
		if isAgent(p) {
			agents = append(agents, agentOf(ctx, machine, c, p, projects))
		}
	}
	sortAgents(agents)
	return agents, nil
}

// stateOrder puts what needs the person first.
var stateOrder = map[string]int{StateWaiting: 0, StateDone: 1, StateWorking: 2, StateIdle: 3}

func sortAgents(agents []Agent) {
	sort.SliceStable(agents, func(i, j int) bool {
		a, b := agents[i], agents[j]
		if stateOrder[a.State] != stateOrder[b.State] {
			return stateOrder[a.State] < stateOrder[b.State]
		}
		return a.Since.Before(b.Since)
	})
}

// isRunning reports whether a pane is one the pane list carries.
func isRunning(p proto.PaneInfo) bool { return p.State == proto.PaneRunning }

// buildPane is any running pane as the phone sees it.
func buildPane(machine string, p proto.PaneInfo, projects map[string]string, screen []string) Pane {
	if p.Agent != nil {
		return Pane{Agent: buildAgent(machine, p, projects, screen), Kind: KindAgent, Cwd: p.Cwd}
	}
	a := Agent{Machine: machine, Pane: composePaneID(machine, p.ID), Name: p.Name, Branch: p.Branch, State: StateIdle,
		Since: p.Created.UTC(), Title: p.Title, CreatedBy: p.CreatedBy}
	if p.ProjectID != "" {
		a.Project = &ProjectRef{ID: p.ProjectID, Name: projects[p.ProjectID]}
	}
	return Pane{Agent: a, Kind: KindTerminal, Cwd: p.Cwd}
}

// paneOf builds one pane, reading its screen when its agent waits.
func paneOf(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string) Pane {
	var screen proto.PaneReadResult
	if p.Agent != nil && p.Agent.State == proto.AgentBlocked {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: p.ID}, &screen)
	}
	return buildPane(machine, p, projects, screen.Lines)
}

// paneList is every running pane: the agents in the list's order, then
// the terminals, oldest first.
func paneList(ctx context.Context, machine string, c caller) ([]Pane, error) {
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		return nil, err
	}
	projects, err := projectNames(ctx, c)
	if err != nil {
		return nil, err
	}
	var agents []Agent
	byPane := map[string]Pane{}
	var terminals []Pane
	for _, p := range list.Panes {
		if !isRunning(p) {
			continue
		}
		pn := paneOf(ctx, machine, c, p, projects)
		if pn.Kind == KindAgent {
			agents = append(agents, pn.Agent)
			byPane[pn.Pane] = pn // keyed as the phone sees it, which is how it is looked up
		} else {
			terminals = append(terminals, pn)
		}
	}
	sortAgents(agents)
	panes := []Pane{}
	for _, a := range agents {
		panes = append(panes, byPane[a.Pane])
	}
	sort.SliceStable(terminals, func(i, j int) bool { return terminals[i].Since.Before(terminals[j].Since) })
	return append(panes, terminals...), nil
}
