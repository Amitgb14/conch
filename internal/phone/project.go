package phone

import (
	"slices"
	"strings"

	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// projectDetail is GET /api/project: the TUI's project page for a phone,
// from the same two calls the TUI makes — project.list, which carries each
// worktree, each branch's pull request and its checks, and pane.list, whose
// agents say where they work and what they have spent. Nothing is asked of
// git or gh here: the server already did, and keeps doing so.
func (g *Gateway) projectDetail(rq *request) (any, *APIError) {
	q := rq.r.URL.Query()
	id := q.Get("project")
	if id == "" {
		return nil, apiErr(CodeBadRequest, "which project? ?project= is missing")
	}
	c, machine, aerr := g.machineFor(rq.dev, q.Get("machine"))
	if aerr != nil {
		return nil, aerr
	}
	var list proto.ProjectList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodProjectList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Projects, func(p proto.ProjectInfo) bool { return p.ID == id })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no project "+id+" on "+machine)
	}
	// The panes only add to the page: a project whose agents could not be
	// listed is still its worktrees and pull requests.
	var panes proto.PaneList
	if g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &panes) != nil {
		panes.Panes = nil
	}
	// A conch without pull requests says nothing of them; one that somehow
	// sent some anyway is not believed past what it announced.
	prs := len(c.MissingCapabilities([]string{capProjectPR})) == 0
	return buildProjectDetail(machine, list.Projects[i], panes.Panes, prs), nil
}

// capProjectPR is pull requests on project.list. It is older than the
// protocol's named capabilities, so proto has only the string.
const capProjectPR = "project.pr.v1"

// buildProjectDetail is the page from what the server said. prs false
// leaves pull requests out, for a server that cannot have them.
func buildProjectDetail(machine string, proj proto.ProjectInfo, panes []proto.PaneInfo, prs bool) ProjectDetail {
	out := ProjectDetail{
		Project:   Project{ID: proj.ID, Name: proj.Name, Path: proj.Path, Base: proj.Base},
		Worktrees: []Worktree{},
		PRs:       []PullRequest{},
	}
	branches := map[string]proto.BranchInfo{}
	for _, b := range proj.Branches {
		branches[b.Name] = b
	}
	var agents []proto.PaneInfo
	for _, p := range panes {
		if p.ProjectID == proj.ID && isAgent(p) {
			agents = append(agents, p)
		}
	}
	for _, wt := range proj.Worktrees {
		w := Worktree{Branch: wt.Branch, Path: wt.Path, Base: wt.Main}
		if wt.Status != nil {
			w.Added, w.Removed, w.Dirty = wt.Status.Added, wt.Status.Deleted, !wt.Status.Clean()
		}
		if b, ok := branches[wt.Branch]; ok && wt.Branch != "" && !wt.Detached {
			w.Ahead, w.Behind = b.BaseAhead, b.BaseBehind
			if prs && b.PR != nil {
				w.PR = b.PR.Number
			}
		}
		for _, p := range agents {
			if wt.Branch != "" && !wt.Detached && p.Branch == wt.Branch {
				w.Panes = append(w.Panes, fleet.ComposePaneID(machine, p.ID))
			}
		}
		out.Worktrees = append(out.Worktrees, w)
	}
	if prs {
		for _, b := range proj.Branches {
			if b.PR != nil {
				out.PRs = append(out.PRs, pullRequest(b.Name, *b.PR))
			}
		}
	}
	out.Usage = projectUsage(agents)
	return out
}

// pullRequest is the server's PRInfo in the contract's words.
func pullRequest(branch string, pr proto.PRInfo) PullRequest {
	out := PullRequest{Number: pr.Number, Title: pr.Title, Branch: branch, URL: pr.URL,
		State: prState(pr), Checks: []PRCheck{}, Review: reviewWords(pr.Review)}
	for _, r := range pr.Runs {
		out.Checks = append(out.Checks, PRCheck{Name: r.Name, State: r.State})
	}
	return out
}

func prState(pr proto.PRInfo) string {
	switch pr.State {
	case "MERGED":
		return PRMerged
	case "CLOSED":
		return PRClosed
	}
	if pr.Draft {
		return PRDraft
	}
	return PROpen
}

// reviewWords is GitHub's review decision as a person says it.
func reviewWords(decision string) string {
	if decision == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(decision), "_", " ")
}

// projectUsage sums what the agents reported, as the TUI's project page
// does; nil when none has reported anything.
func projectUsage(agents []proto.PaneInfo) *ProjectUsage {
	var u ProjectUsage
	for _, p := range agents {
		t := p.Agent.Tokens
		if t == nil {
			continue
		}
		u.Agents++
		u.InputTokens += t.Input + t.CacheRead + t.CacheWrite
		u.OutputTokens += t.Output
		if t.CostUSD > 0 {
			u.CostUSD += t.CostUSD
			u.CostAgents++
		}
	}
	if u.Agents == 0 || u.InputTokens == 0 && u.OutputTokens == 0 && u.CostUSD == 0 {
		return nil
	}
	return &u
}
