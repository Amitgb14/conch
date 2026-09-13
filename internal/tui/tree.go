package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/amitghadge/conch/internal/proto"
)

// Node kinds in the sidebar tree.
type nodeKind int

const (
	kindMachine nodeKind = iota
	kindProject
	kindBranches  // section
	kindAgents    // section
	kindTerminals // section
	kindBranch
	kindMore // "… N more" branches
	kindPane
)

// row is one visible line of the sidebar tree. IDs of panes and projects
// are only unique per machine, so every row carries its machine.
type row struct {
	id        string
	kind      nodeKind
	depth     int
	machine   string
	projectID string
	branch    string
	paneID    string
	count     int // sections: children; more: hidden branches
}

func (r row) expandable() bool {
	switch r.kind {
	case kindMachine, kindProject, kindBranches, kindAgents, kindTerminals:
		return true
	}
	return false
}

// recentBranchAge is how recent a branch's last commit must be to be listed
// without asking for all branches.
const recentBranchAge = 14 * 24 * time.Hour

const maxListedBranches = 12

const localMachine = "local"

// treeMachine is one machine's data for the tree.
type treeMachine struct {
	id       string
	panes    []proto.PaneInfo
	projects []proto.ProjectInfo
	agents   map[string]bool // panes that have ever run an agent
}

// treeInput is everything the tree is built from.
type treeInput struct {
	machines []treeMachine
	expanded map[string]bool // explicit expand/collapse choices
	showAll  map[string]bool // scoped project IDs listing every branch
	filter   string
	now      time.Time
}

// scoped makes a per-machine ID unique across machines. Local IDs stay as
// they are, so fold state saved before remote machines existed still applies.
func scoped(machine, id string) string {
	if machine == localMachine {
		return id
	}
	return machine + "~" + id
}

func machineID(mid string) string            { return "m:" + mid }
func projectNodeID(mid, pid string) string   { return "p:" + scoped(mid, pid) }
func sectionID(mid, pid, s string) string    { return "p:" + scoped(mid, pid) + "/" + s }
func branchNodeID(mid, pid, b string) string { return "b:" + scoped(mid, pid) + ":" + b }
func paneNodeID(mid, id string) string       { return "pane:" + scoped(mid, id) }
func moreID(mid, pid string) string          { return "more:" + scoped(mid, pid) }
func looseTerminalsID(mid string) string     { return machineID(mid) + "/terminals" }

// buildTree flattens the visible tree into rows.
func buildTree(in treeInput) []row {
	filter := strings.ToLower(strings.TrimSpace(in.filter))
	match := func(s string) bool { return filter == "" || strings.Contains(strings.ToLower(s), filter) }
	open := func(id string, def bool) bool {
		if filter != "" {
			return true // show every match
		}
		if v, ok := in.expanded[id]; ok {
			return v
		}
		return def
	}
	var rows []row
	for _, mach := range in.machines {
		rows = append(rows, machineRows(in, mach, filter, match, open)...)
	}
	return rows
}

func machineRows(in treeInput, mach treeMachine, filter string, match func(string) bool, open func(string, bool) bool) []row {
	mid := mach.id
	known := map[string]bool{}
	for _, p := range mach.projects {
		known[p.ID] = true
	}
	byProject := map[string][]proto.PaneInfo{}
	var loose []proto.PaneInfo
	for _, p := range mach.panes {
		if known[p.ProjectID] {
			byProject[p.ProjectID] = append(byProject[p.ProjectID], p)
		} else {
			loose = append(loose, p)
		}
	}
	isAgent := func(p proto.PaneInfo) bool { return p.Agent != nil || mach.agents[p.ID] }

	projects := append([]proto.ProjectInfo(nil), mach.projects...)
	sort.SliceStable(projects, func(i, j int) bool {
		// Projects with panes first, then by name.
		ai, aj := len(byProject[projects[i].ID]) > 0, len(byProject[projects[j].ID]) > 0
		if ai != aj {
			return ai
		}
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})

	paneRows := func(panes []proto.PaneInfo, depth int, projectMatched bool) []row {
		var out []row
		for _, p := range panes {
			if projectMatched || match(p.DisplayName()) || match(p.Branch) {
				out = append(out, row{id: paneNodeID(mid, p.ID), kind: kindPane, depth: depth, machine: mid,
					projectID: p.ProjectID, branch: p.Branch, paneID: p.ID})
			}
		}
		return out
	}

	var body []row
	for _, proj := range projects {
		panes := byProject[proj.ID]
		var agents, terms []proto.PaneInfo
		for _, p := range panes {
			if isAgent(p) {
				agents = append(agents, p)
			} else {
				terms = append(terms, p)
			}
		}
		projMatched := filter != "" && match(proj.Name)

		var children []row
		if proj.Git {
			all := in.showAll[scoped(mid, proj.ID)] || filter != ""
			branches, hidden := listedBranches(proj, panes, all, in.now)
			var brows []row
			for _, b := range branches {
				if projMatched || match(b.Name) {
					brows = append(brows, row{id: branchNodeID(mid, proj.ID, b.Name), kind: kindBranch, depth: 3,
						machine: mid, projectID: proj.ID, branch: b.Name})
				}
			}
			if hidden > 0 {
				brows = append(brows, row{id: moreID(mid, proj.ID), kind: kindMore, depth: 3, machine: mid, projectID: proj.ID, count: hidden})
			}
			if filter == "" || len(brows) > 0 {
				sid := sectionID(mid, proj.ID, "branches")
				children = append(children, row{id: sid, kind: kindBranches, depth: 2, machine: mid, projectID: proj.ID, count: len(proj.Branches)})
				if open(sid, true) {
					children = append(children, brows...)
				}
			}
		}
		for _, sec := range []struct {
			name  string
			kind  nodeKind
			panes []proto.PaneInfo
		}{{"agents", kindAgents, agents}, {"terminals", kindTerminals, terms}} {
			prows := paneRows(sec.panes, 3, projMatched)
			if len(prows) == 0 {
				continue
			}
			sid := sectionID(mid, proj.ID, sec.name)
			children = append(children, row{id: sid, kind: sec.kind, depth: 2, machine: mid, projectID: proj.ID, count: len(sec.panes)})
			if open(sid, true) {
				children = append(children, prows...)
			}
		}

		if filter != "" && !projMatched && len(children) == 0 {
			continue
		}
		pid := projectNodeID(mid, proj.ID)
		body = append(body, row{id: pid, kind: kindProject, depth: 1, machine: mid, projectID: proj.ID})
		if open(pid, len(panes) > 0) {
			body = append(body, children...)
		}
	}

	// Panes outside any project, split like a project's.
	var looseAgents, looseTerms []proto.PaneInfo
	for _, p := range loose {
		if isAgent(p) {
			looseAgents = append(looseAgents, p)
		} else {
			looseTerms = append(looseTerms, p)
		}
	}
	for _, sec := range []struct {
		id    string
		kind  nodeKind
		panes []proto.PaneInfo
	}{{machineID(mid) + "/agents", kindAgents, looseAgents}, {looseTerminalsID(mid), kindTerminals, looseTerms}} {
		if prows := paneRows(sec.panes, 2, false); len(prows) > 0 {
			body = append(body, row{id: sec.id, kind: sec.kind, depth: 1, machine: mid, count: len(sec.panes)})
			if open(sec.id, true) {
				body = append(body, prows...)
			}
		}
	}

	if filter != "" && len(body) == 0 {
		return nil
	}
	rows := []row{{id: machineID(mid), kind: kindMachine, machine: mid}}
	if open(machineID(mid), true) {
		rows = append(rows, body...)
	}
	return rows
}

// listedBranches picks the branches worth showing: checked-out ones (main
// worktree first), the base, branches panes work on, and recently committed
// ones. With all set every branch is listed. hidden counts the rest.
func listedBranches(proj proto.ProjectInfo, panes []proto.PaneInfo, all bool, now time.Time) (list []proto.BranchInfo, hidden int) {
	mainBranch := ""
	for _, wt := range proj.Worktrees {
		if wt.Main {
			mainBranch = wt.Branch
		}
	}
	withPane := map[string]bool{}
	for _, p := range panes {
		withPane[p.Branch] = true
	}
	rank := func(b proto.BranchInfo) int {
		switch {
		case b.Name == mainBranch:
			return 0
		case b.Worktree != "":
			return 1
		case b.Name == proj.Base || withPane[b.Name]:
			return 2
		}
		return 3
	}
	branches := append([]proto.BranchInfo(nil), proj.Branches...)
	sort.SliceStable(branches, func(i, j int) bool { return rank(branches[i]) < rank(branches[j]) })

	recent := 0
	for _, b := range branches {
		keep := all || rank(b) < 3
		if !keep && now.Sub(b.Committed) < recentBranchAge && recent < maxListedBranches {
			keep = true
			recent++
		}
		if keep {
			list = append(list, b)
		} else {
			hidden++
		}
	}
	return list, hidden
}

// parentID returns the id of the row's parent in rows, or "".
func parentID(rows []row, i int) string {
	for j := i - 1; j >= 0; j-- {
		if rows[j].depth < rows[i].depth {
			return rows[j].id
		}
	}
	return ""
}

func indexOfRow(rows []row, id string) int {
	for i, r := range rows {
		if r.id == id {
			return i
		}
	}
	return -1
}
