package tui

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
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
	kindSessions  // section: saved agent sessions
	kindCLI       // a machine's agents and terminals outside every project
	kindWorkspace // a machine's projects
	kindSSH       // section: a machine's ssh sessions to other hosts
	// kindReviewQueue is the cross-machine list of what needs a decision.
	// It has no tree row; Q opens it in the focused split.
	kindReviewQueue
	// kindSavedSSH is an ssh host kept in the tree with no session open to
	// it. Saved tabs store kinds by number, so it keeps its place.
	kindSavedSSH
	// kindFiles is a project's file explorer. Kinds are saved in ui.json by
	// number, so new ones go last.
	kindFiles
	// kindSandboxes groups the machines conch made itself, and
	// kindSandboxProvider one provider's own, so several sandboxes sit
	// under Sandboxes → Daytona → … rather than filling the tree's top.
	kindSandboxes
	kindSandboxProvider
	// kindTab groups a project's panes by the tab they are open in, with
	// [ui] tree_groups = "tabs". Kinds are saved in ui.json by number, so
	// new ones go last.
	kindTab
	// kindFolder is a group of your own inside a pane section (folders.go).
	// Kinds are saved in ui.json by number, so new ones go last.
	kindFolder
	// kindSubagent is an agent running inside another agent's process
	// (Claude's Agent tool), listed under it. It has no pane: on screen it
	// is the agent that runs it. Kinds are saved in ui.json by number, so
	// new ones go last.
	kindSubagent
)

// foldered splits a section's panes into the folders of that section and
// what is left over. A folder with nothing in it is still listed, so one
// made and not filled yet does not vanish. The panes outside them go first
// and the folders after: a pane listed below an open folder, one step out
// from what is in it, read as more of the folder's contents.
func foldered(in treeInput, mid, pid string, kind nodeKind, panes []proto.PaneInfo,
	depth int, projMatched bool, rows func([]proto.PaneInfo, int, bool) []row,
	open func(string, bool) bool) (out []row, loose []proto.PaneInfo) {
	fs := in.folders[folderKey(mid, pid, kind)]
	if len(fs) == 0 {
		return nil, panes
	}
	taken := map[string]bool{}
	for _, f := range fs {
		mine := f.claim(panes, taken)
		fid := folderRowID(mid, pid, kind, f.Name)
		prows := rows(mine, depth+1, projMatched)
		if in.filter != "" && len(prows) == 0 {
			continue // narrowed away with everything in it
		}
		out = append(out, row{id: fid, kind: kindFolder, depth: depth, machine: mid, projectID: pid,
			count: len(mine), label: f.Name, section: kind})
		if open(fid, false) { // folded until opened: what follows an open one is its own
			out = append(out, prows...)
		}
	}
	for _, p := range panes {
		if !taken[p.ID] {
			loose = append(loose, p)
		}
	}
	return out, loose
}

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
	count     int      // sections: children; more: hidden branches
	label     string   // a tab section's name, as the bar writes it
	tabIndex  int      // a tab section: which tab it stands for
	section   nodeKind // a folder: the section it sits in
	kids      int      // a pane: how many panes it started, and subagents it runs, are listed under it
	nested    bool     // a pane: listed under the pane that started it
}

func (r row) expandable() bool {
	if r.kind == kindPane {
		return r.kids > 0 // an agent with helpers under it folds them away
	}
	switch r.kind {
	case kindMachine, kindWorkspace, kindProject, kindBranches, kindAgents, kindTerminals, kindCLI, kindSSH,
		kindSandboxes, kindSandboxProvider, kindTab, kindFolder:
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
	id string
	// sandbox is the provider that made this machine, if conch did:
	// "daytona" and so on. Ordinary machines have none.
	sandbox  string
	label    string
	panes    []proto.PaneInfo
	projects []proto.ProjectInfo
	agents   map[string]bool        // panes that have ever run an agent
	sessions bool                   // the server lists saved sessions
	savedSSH []string               // ssh hosts kept in the tree (this computer only)
	sshInfo  map[string]sshHostInfo // saved hosts' names and options
}

// treeInput is everything the tree is built from.
// treeTab is one tab of the bar, for grouping a project's panes by the tab
// each is open in. The tree is a pure function of its input, so the layout
// arrives as data rather than the tree reaching into the model.
type treeTab struct {
	machine   string
	projectID string   // whose project's section it belongs under
	index     int      // where it is in the model's tabs
	label     string   // what the bar shows, a renamed tab included
	n         int      // its number in the bar
	splits    bool     // more than one split in it
	panes     []string // pane IDs, in the order the tab holds them
}

type treeInput struct {
	machines []treeMachine
	tabs     []treeTab                // empty unless [ui] tree_groups = "tabs"
	folders  map[string][]savedFolder // groups of your own, by section key
	expanded map[string]bool          // explicit expand/collapse choices
	showAll  map[string]bool          // scoped project IDs listing every branch
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

func machineID(mid string) string          { return "m:" + mid }
func sandboxesID() string                  { return "sandboxes" }
func sandboxProviderID(p string) string    { return "sandboxes/" + p }
func projectNodeID(mid, pid string) string { return "p:" + scoped(mid, pid) }
func folderRowID(mid, pid string, kind nodeKind, name string) string {
	return "folder:" + folderKey(mid, pid, kind) + "/" + name
}

func sectionID(mid, pid, s string) string    { return "p:" + scoped(mid, pid) + "/" + s }
func branchNodeID(mid, pid, b string) string { return "b:" + scoped(mid, pid) + ":" + b }
func paneNodeID(mid, id string) string       { return "pane:" + scoped(mid, id) }
func subagentID(mid, pane, id string) string { return paneNodeID(mid, pane) + "/sub/" + id }
func moreID(mid, pid string) string          { return "more:" + scoped(mid, pid) }
func looseTerminalsID(mid string) string     { return machineID(mid) + "/terminals" }
func looseSSHID(mid string) string           { return machineID(mid) + "/ssh" }
func savedSSHID(target string) string        { return "sshsaved:" + target }
func cliID(mid string) string                { return machineID(mid) + "/cli" }
func workspaceID(mid string) string          { return machineID(mid) + "/workspace" }

// buildTree flattens the visible tree into rows.
func buildTree(in treeInput) []row {
	filter := strings.ToLower(strings.TrimSpace(in.filter))
	// "!" filters to the agents waiting for an answer, on every machine;
	// nothing is matched by name then.
	waiting := filter == waitingFilter
	match := func(s string) bool {
		return !waiting && (filter == "" || strings.Contains(strings.ToLower(s), filter))
	}
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
	var boxes []treeMachine
	for _, mach := range in.machines {
		if mach.sandbox != "" {
			boxes = append(boxes, mach)
			continue
		}
		rows = append(rows, machineRows(in, mach, filter, waiting, match, open)...)
	}
	return append(rows, sandboxRows(in, boxes, filter, waiting, match, open)...)
}

// sandboxRows groups the machines conch made under Sandboxes → provider →
// sandbox, so a fleet of them doesn't fill the top of the tree. With none
// there is no group at all: M makes the first.
func sandboxRows(in treeInput, boxes []treeMachine, filter string, waiting bool, match func(string) bool, open func(string, bool) bool) []row {
	if len(boxes) == 0 {
		return nil
	}
	byProvider := map[string][]treeMachine{}
	var order []string
	for _, mach := range boxes {
		if _, seen := byProvider[mach.sandbox]; !seen {
			order = append(order, mach.sandbox)
		}
		byProvider[mach.sandbox] = append(byProvider[mach.sandbox], mach)
	}
	sort.Strings(order) // the providers conch knows, then any it doesn't
	var body []row
	total := 0
	for _, provider := range order {
		machines := byProvider[provider]
		sort.Slice(machines, func(i, j int) bool { return machines[i].label < machines[j].label })
		var kids []row
		for _, mach := range machines {
			rows := machineRows(in, mach, filter, waiting, match, open)
			for i := range rows {
				rows[i].depth += 2 // under Sandboxes → provider
			}
			kids = append(kids, rows...)
		}
		if len(kids) == 0 {
			continue // filtered out
		}
		total += len(machines)
		sid := sandboxProviderID(provider)
		row := row{id: sid, kind: kindSandboxProvider, depth: 1, count: len(machines), branch: provider}
		body = append(body, row)
		if open(sid, true) {
			body = append(body, kids...)
		}
	}
	if len(body) == 0 {
		return nil
	}
	rows := []row{{id: sandboxesID(), kind: kindSandboxes, count: total}}
	if open(sandboxesID(), true) {
		rows = append(rows, body...)
	}
	return rows
}

// waitingFilter is the filter text that keeps only agents waiting for the
// user, wherever they are.
const waitingFilter = "!"

func machineRows(in treeInput, mach treeMachine, filter string, waiting bool, match func(string) bool, open func(string, bool) bool) []row {
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

	paneRow := func(p proto.PaneInfo, depth, kids int, nested bool) row {
		return row{id: paneNodeID(mid, p.ID), kind: kindPane, depth: depth, machine: mid,
			projectID: p.ProjectID, branch: p.Branch, paneID: p.ID, kids: kids, nested: nested}
	}
	// paneRows lists panes, with the ones an agent started under the agent
	// that started it: a helper is somebody's helper, and reading the shape
	// beats reading it off a chip on every row. A pane whose creator is not
	// in the same list — another section, another machine, or filtered away
	// — is listed where it would have been, with the ↳ chip it always had.
	paneRows := func(panes []proto.PaneInfo, depth int, projectMatched bool) []row {
		shown := make([]proto.PaneInfo, 0, len(panes))
		for _, p := range panes {
			if waiting {
				if p.Agent.NeedsAttention() {
					shown = append(shown, p)
				}
				continue
			}
			if projectMatched || match(p.DisplayName()) || match(p.Branch) {
				shown = append(shown, p)
			}
		}
		here := make(map[string]bool, len(shown))
		for _, p := range shown {
			here[p.ID] = true
		}
		kids := map[string][]proto.PaneInfo{}
		for _, p := range shown {
			if p.CreatedBy != "" && here[p.CreatedBy] && p.CreatedBy != p.ID {
				kids[p.CreatedBy] = append(kids[p.CreatedBy], p)
			}
		}
		root := func(p proto.PaneInfo) bool {
			return p.CreatedBy == "" || !here[p.CreatedBy] || p.CreatedBy == p.ID
		}
		// Which panes have a place in the forest at all, folded or not:
		// folding an agent hides its helpers, it does not set them loose.
		placed := map[string]bool{}
		var walk func(p proto.PaneInfo)
		walk = func(p proto.PaneInfo) {
			if placed[p.ID] {
				return // a pane that descends from itself stops here
			}
			placed[p.ID] = true
			for _, k := range kids[p.ID] {
				walk(k)
			}
		}
		for _, p := range shown {
			if root(p) {
				walk(p)
			}
		}
		var out []row
		var emit func(p proto.PaneInfo, d int, seen map[string]bool)
		emit = func(p proto.PaneInfo, d int, seen map[string]bool) {
			if seen[p.ID] {
				return
			}
			seen[p.ID] = true
			subs := subagentRows(mid, p, d+1)
			out = append(out, paneRow(p, d, len(kids[p.ID])+len(subs), d > depth))
			if len(kids[p.ID])+len(subs) == 0 || !open(paneNodeID(mid, p.ID), true) {
				return
			}
			for _, k := range kids[p.ID] {
				emit(k, d+1, seen)
			}
			out = append(out, subs...)
		}
		seen := map[string]bool{}
		for _, p := range shown {
			if root(p) {
				emit(p, depth, seen)
			}
		}
		// A ring of panes each started by the next belongs to no root, and
		// is listed rather than lost.
		for _, p := range shown {
			if !placed[p.ID] {
				emit(p, depth, seen)
			}
		}
		return out
	}

	// Projects, grouped under Workspace.
	var workspace []row
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
		// Grouped by tab: a section per tab holding the panes open in it,
		// above the rest. A pane in no tab keeps its Agents or Terminals
		// section below, so closing a tab never hides one.
		inTab := map[string]bool{}
		byID := make(map[string]proto.PaneInfo, len(panes))
		for _, p := range panes {
			byID[p.ID] = p
		}
		for _, tb := range in.tabs {
			if tb.machine != mid || tb.projectID != proj.ID {
				continue
			}
			var prows []row
			npanes := 0
			for _, id := range tb.panes {
				p, ok := byID[id]
				if !ok {
					continue
				}
				inTab[id] = true
				if waiting && !p.Agent.NeedsAttention() {
					continue
				}
				if !waiting && !(projMatched || match(p.DisplayName()) || match(p.Branch)) {
					continue
				}
				npanes++
				subs := subagentRows(mid, p, 5)
				prows = append(prows, row{id: paneNodeID(mid, p.ID), kind: kindPane, depth: 4, machine: mid,
					projectID: p.ProjectID, branch: p.Branch, paneID: p.ID, kids: len(subs)})
				if len(subs) > 0 && open(paneNodeID(mid, p.ID), true) {
					prows = append(prows, subs...)
				}
			}
			if len(prows) == 0 {
				continue
			}
			sid := sectionID(mid, proj.ID, "tab/"+itoa(tb.n))
			children = append(children, row{id: sid, kind: kindTab, depth: 3, machine: mid, projectID: proj.ID,
				count: npanes, label: tb.label, tabIndex: tb.index})
			if open(sid, true) {
				children = append(children, prows...)
			}
		}
		if len(inTab) > 0 {
			keep := func(ps []proto.PaneInfo) []proto.PaneInfo {
				var out []proto.PaneInfo
				for _, p := range ps {
					if !inTab[p.ID] {
						out = append(out, p)
					}
				}
				return out
			}
			agents, terms = keep(agents), keep(terms)
		}
		if proj.Git {
			all := in.showAll[scoped(mid, proj.ID)] || filter != ""
			branches, hidden := listedBranches(proj, panes, all, in.now)
			var brows []row
			for _, b := range branches {
				if projMatched || match(b.Name) {
					brows = append(brows, row{id: branchNodeID(mid, proj.ID, b.Name), kind: kindBranch, depth: 4,
						machine: mid, projectID: proj.ID, branch: b.Name})
				}
			}
			if hidden > 0 {
				brows = append(brows, row{id: moreID(mid, proj.ID), kind: kindMore, depth: 4, machine: mid, projectID: proj.ID, count: hidden})
			}
			if filter == "" || len(brows) > 0 {
				sid := sectionID(mid, proj.ID, "branches")
				children = append(children, row{id: sid, kind: kindBranches, depth: 3, machine: mid, projectID: proj.ID, count: len(proj.Branches)})
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
			frows, loose := foldered(in, mid, proj.ID, sec.kind, sec.panes, 4, projMatched, paneRows, open)
			prows := paneRows(loose, 4, projMatched)
			if len(prows) == 0 && len(frows) == 0 {
				continue
			}
			sid := sectionID(mid, proj.ID, sec.name)
			children = append(children, row{id: sid, kind: sec.kind, depth: 3, machine: mid, projectID: proj.ID, count: len(sec.panes)})
			if open(sid, true) {
				// Folders first, then what is in none of them: a folder is
				// something you put there, and it should not be below the
				// list it was made to tidy.
				children = append(children, frows...)
				children = append(children, prows...)
			}
		}

		if filter == "" {
			children = append(children, row{id: sectionID(mid, proj.ID, "files"), kind: kindFiles, depth: 3, machine: mid, projectID: proj.ID})
		}
		if mach.sessions && filter == "" {
			children = append(children, row{id: sectionID(mid, proj.ID, "sessions"), kind: kindSessions, depth: 3, machine: mid, projectID: proj.ID})
		}

		if filter != "" && !projMatched && len(children) == 0 {
			continue
		}
		pid := projectNodeID(mid, proj.ID)
		workspace = append(workspace, row{id: pid, kind: kindProject, depth: 2, machine: mid, projectID: proj.ID})
		if open(pid, len(panes) > 0) {
			workspace = append(workspace, children...)
		}
	}
	var body []row
	if len(workspace) > 0 {
		body = append(body, row{id: workspaceID(mid), kind: kindWorkspace, depth: 1, machine: mid, count: len(mach.projects)})
		if open(workspaceID(mid), true) {
			body = append(body, workspace...)
		}
	}

	// Panes outside any project, grouped under CLI and split like a
	// project's.
	var looseAgents, looseTerms, looseSSH []proto.PaneInfo
	var cli []row
	for _, p := range loose {
		switch {
		case isAgent(p):
			looseAgents = append(looseAgents, p)
		case sshPane(p):
			looseSSH = append(looseSSH, p)
		default:
			looseTerms = append(looseTerms, p)
		}
	}
	sshRows, savedCount := sshSectionRows(in, mach, looseSSH, match, open, paneRows)
	for _, sec := range []struct {
		id    string
		kind  nodeKind
		panes []proto.PaneInfo
		rows  []row
		count int
	}{
		{machineID(mid) + "/agents", kindAgents, looseAgents, nil, len(looseAgents)},
		{looseTerminalsID(mid), kindTerminals, looseTerms, nil, len(looseTerms)},
		{looseSSHID(mid), kindSSH, looseSSH, sshRows, len(looseSSH) + savedCount},
	} {
		prows := sec.rows
		if sec.kind != kindSSH {
			frows, loose := foldered(in, mid, "", sec.kind, sec.panes, 3, false, paneRows, open)
			prows = append(frows, paneRows(loose, 3, false)...)
		}
		if len(prows) > 0 {
			cli = append(cli, row{id: sec.id, kind: sec.kind, depth: 2, machine: mid, count: sec.count})
			if open(sec.id, true) {
				cli = append(cli, prows...)
			}
		}
	}
	if len(cli) > 0 {
		body = append(body, row{id: cliID(mid), kind: kindCLI, depth: 1, machine: mid, count: len(loose) + savedCount})
		if open(cliID(mid), true) {
			body = append(body, cli...)
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

// sshSectionRows are the rows under a machine's SSH section: its folders
// (folders.go), then the sessions and idle saved hosts in none. A folder
// there holds saved hosts as well as panes: a host's sessions are listed in
// its folder, and the host itself while no session is open to it. A host's
// place is settled before any pane's, so a session is never split from its
// host by a pane member naming it elsewhere. saved counts the idle saved
// hosts listed.
func sshSectionRows(in treeInput, mach treeMachine, sessions []proto.PaneInfo, match func(string) bool,
	open func(string, bool) bool, paneRows func([]proto.PaneInfo, int, bool) []row) (rows []row, saved int) {
	mid := mach.id
	idle := idleSavedSSH(mach.savedSSH, sessions)
	hostRows := func(targets []string, depth int) []row {
		var out []row
		for _, t := range targets {
			if match(t) || match(sshName(t)) || match(mach.sshInfo[t].Name) {
				out = append(out, row{id: savedSSHID(t), kind: kindSavedSSH, depth: depth, machine: mid})
			}
		}
		return out
	}
	fs := in.folders[folderKey(mid, "", kindSSH)]
	folderOf := map[string]int{}
	for i, f := range fs {
		for _, mem := range f.Members {
			if _, seen := folderOf[mem.Host]; mem.Host != "" && !seen && slices.Contains(mach.savedSSH, mem.Host) {
				folderOf[mem.Host] = i
			}
		}
	}
	taken := map[string]bool{}
	mine := make([][]proto.PaneInfo, len(fs))
	for _, p := range sessions {
		if i, ok := folderOf[sshTarget(p)]; ok {
			mine[i] = append(mine[i], p)
			taken[p.ID] = true
		}
	}
	for i, f := range fs {
		mine[i] = append(mine[i], f.claim(sessions, taken)...)
	}
	for i, f := range fs {
		var hosts []string
		for _, t := range idle {
			if j, ok := folderOf[t]; ok && j == i {
				hosts = append(hosts, t)
			}
		}
		hrows := hostRows(hosts, 4)
		saved += len(hrows)
		kids := append(paneRows(mine[i], 4, false), hrows...)
		if in.filter != "" && len(kids) == 0 {
			continue // narrowed away with everything in it
		}
		fid := folderRowID(mid, "", kindSSH, f.Name)
		rows = append(rows, row{id: fid, kind: kindFolder, depth: 3, machine: mid, count: len(mine[i]) + len(hosts),
			label: f.Name, section: kindSSH})
		if open(fid, false) { // folded until opened: what follows an open one is its own
			rows = append(rows, kids...)
		}
	}
	var loose []proto.PaneInfo
	for _, p := range sessions {
		if !taken[p.ID] {
			loose = append(loose, p)
		}
	}
	var hosts []string
	for _, t := range idle {
		if _, ok := folderOf[t]; !ok {
			hosts = append(hosts, t)
		}
	}
	hrows := hostRows(hosts, 3)
	return append(append(rows, paneRows(loose, 3, false)...), hrows...), saved + len(hrows)
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

// subagentRows lists the subagents a pane's agent runs, to go under it.
func subagentRows(mid string, p proto.PaneInfo, depth int) []row {
	if p.Agent == nil || p.State != proto.PaneRunning {
		return nil
	}
	var out []row
	for _, a := range p.Agent.Subagents {
		out = append(out, row{id: subagentID(mid, p.ID, a.ID), kind: kindSubagent, depth: depth, machine: mid,
			projectID: p.ProjectID, branch: p.Branch, paneID: p.ID, label: a.ID, nested: true})
	}
	return out
}

// agentRowOf is the row of the agent running a subagent, which is what is
// shown for it, having no screen of its own. Any other row is itself.
func (m *Model) agentRowOf(r row) row {
	if r.kind != kindSubagent {
		return r
	}
	if i := indexOfRow(m.rows, paneNodeID(r.machine, r.paneID)); i >= 0 {
		return m.rows[i]
	}
	return r
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

// treeGroups is how a project's panes are grouped in the tree: "sections"
// unless "tabs" was asked for. Anything else is sections, so a config from
// a newer conch does not leave the tree empty.
func treeGroups(s string) string {
	if s == "tabs" {
		return "tabs"
	}
	return "sections"
}
