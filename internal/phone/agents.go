package phone

import (
	"context"
	"sort"
	"strings"

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

// agentOf builds one pane's agent, reading its screen when it waits and
// the comments waiting for its branch.
func agentOf(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string) Agent {
	return agentWith(ctx, machine, c, p, projects, branchReviews(ctx, c, p))
}

// agentWith is agentOf with the comments already read. A screen that can't
// be read — the pane went as we asked — leaves the question without
// choices rather than failing the list.
func agentWith(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string, reviews []proto.ReviewComment) Agent {
	var screen proto.PaneReadResult
	if p.Agent.State == proto.AgentBlocked {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: p.ID}, &screen)
	}
	a := buildAgent(machine, p, projects, screen.Lines)
	a.Review = reviewNotes(reviews, p)
	return a
}

// branchReviews is the comments waiting for the agent in p: none for a
// pane on no branch, or a reviewer.
func branchReviews(ctx context.Context, c caller, p proto.PaneInfo) []proto.ReviewComment {
	if p.Agent == nil || p.ProjectID == "" || p.Branch == "" || isReviewer(p) {
		return nil
	}
	return reviewsOf(ctx, c, proto.ReviewRef{ProjectID: p.ProjectID, Branch: p.Branch})
}

// isReviewer is a reviewing agent (TUI reviewer.go): the comments are
// for the agent that wrote the work, not for the one reading it.
func isReviewer(p proto.PaneInfo) bool { return strings.HasPrefix(p.Name, "review · ") }

// reviewsOf is the comments a server keeps, none from one without them.
func reviewsOf(ctx context.Context, c caller, ref proto.ReviewRef) []proto.ReviewComment {
	if len(c.MissingCapabilities([]string{proto.CapReviewComments})) > 0 {
		return nil
	}
	var list proto.ReviewList
	if c.Call(ctx, proto.MethodReviewList, ref, &list) != nil {
		return nil
	}
	return list.Comments
}

// reviewNotes is the comments on p's branch, as the phone shows them; a
// reviewer's own pane carries none, since they are not for it.
func reviewNotes(comments []proto.ReviewComment, p proto.PaneInfo) []ReviewNote {
	if p.Agent == nil || p.Branch == "" || isReviewer(p) {
		return nil
	}
	var out []ReviewNote
	for _, rc := range comments {
		if rc.ProjectID != p.ProjectID || rc.Branch != p.Branch {
			continue
		}
		n := ReviewNote{ID: rc.ID, Where: proto.ReviewWhere(rc), Note: rc.Note, By: rc.By,
			GitHub: rc.Source == proto.ReviewGitHub, URL: rc.URL, Outdated: rc.Outdated}
		for _, r := range rc.Replies {
			n.Replies = append(n.Replies, ReviewReply{By: r.By, Note: r.Note})
		}
		out = append(out, n)
	}
	return out
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
	// One read of every branch's comments for the list, not one a pane.
	reviews := reviewsOf(ctx, c, proto.ReviewRef{})
	agents := []Agent{}
	for _, p := range list.Panes {
		if isAgent(p) {
			agents = append(agents, agentWith(ctx, machine, c, p, projects, reviews))
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

// paneOf builds one pane, reading its screen when its agent waits and
// the comments waiting for its branch.
func paneOf(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string) Pane {
	return paneWith(ctx, machine, c, p, projects, branchReviews(ctx, c, p))
}

// paneWith is paneOf with the comments already read.
func paneWith(ctx context.Context, machine string, c caller, p proto.PaneInfo, projects map[string]string, reviews []proto.ReviewComment) Pane {
	var screen proto.PaneReadResult
	if p.Agent != nil && p.Agent.State == proto.AgentBlocked {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: p.ID}, &screen)
	}
	pn := buildPane(machine, p, projects, screen.Lines)
	pn.Review = reviewNotes(reviews, p)
	return pn
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
	reviews := reviewsOf(ctx, c, proto.ReviewRef{})
	var agents []Agent
	byPane := map[string]Pane{}
	var terminals []Pane
	for _, p := range list.Panes {
		if !isRunning(p) {
			continue
		}
		pn := paneWith(ctx, machine, c, p, projects, reviews)
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
