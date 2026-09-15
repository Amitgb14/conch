package tui

import (
	"slices"
)

// Tabs belong to a group of the tree: a project, or a machine's CLI group,
// narrowed to agents or terminals for a tab of panes. The tab bar lists
// the tabs of the group selected in the tree; the machine itself has none.
// When the selection has no tab, a preview (a tab not in the list) shows
// it, and becomes a real tab once used.

type scopeLevel int

const (
	scopeNone    scopeLevel = iota // no group: a new tab waiting for a pick
	scopeMachine                   // the machine row: no tabs
	scopeProject
	scopeCLI
)

type tabScope struct {
	level   scopeLevel
	machine string
	project string   // scopeProject
	section nodeKind // kindAgents, kindTerminals, or 0 for the whole group
}

func (s tabScope) key() string {
	return string(rune('0'+s.level)) + "|" + s.machine + "|" + s.project + "|" + string(rune('0'+s.section))
}

// groupOf is the project or CLI group of something on machine mid in
// project pid, as the tree places it.
func (m Model) groupOf(mid, pid string) tabScope {
	if pid != "" && m.project(mid, pid) != nil {
		return tabScope{level: scopeProject, machine: mid, project: pid}
	}
	return tabScope{level: scopeCLI, machine: mid}
}

// paneSection is where the tree lists a pane: under Agents or Terminals.
func (m Model) paneSection(mid, id string) nodeKind {
	if p := m.pane(mid, id); p != nil && p.Agent != nil {
		return kindAgents
	}
	if mach := m.machine(mid); mach != nil && mach.agents[id] {
		return kindAgents
	}
	return kindTerminals
}

// rowScope is the group a tree row selects.
func (m Model) rowScope(r row) tabScope {
	switch r.kind {
	case kindMachine:
		return tabScope{level: scopeMachine, machine: r.machine}
	case kindCLI:
		return tabScope{level: scopeCLI, machine: r.machine}
	case kindAgents, kindTerminals:
		s := m.groupOf(r.machine, r.projectID)
		s.section = r.kind
		return s
	case kindPane:
		pid := r.projectID
		if p := m.pane(r.machine, r.paneID); p != nil {
			pid = p.ProjectID
		}
		s := m.groupOf(r.machine, pid)
		s.section = m.paneSection(r.machine, r.paneID)
		return s
	}
	return m.groupOf(r.machine, r.projectID)
}

// viewScope is the group a leaf's view belongs to.
func (m Model) viewScope(v viewRef) tabScope {
	if v.empty() {
		return tabScope{}
	}
	return m.rowScope(row{id: v.Row, kind: v.Kind, machine: v.Machine, projectID: v.ProjectID, paneID: v.PaneID, branch: v.Branch})
}

// tabScopeOf is the group of a tab's first split that shows something.
func (m Model) tabScopeOf(t *tab) tabScope {
	for _, l := range t.root.leaves() {
		if !l.view.empty() {
			return m.viewScope(l.view)
		}
	}
	return t.home
}

// inScope reports whether a tab of group s is listed for filter f.
func inScope(f, s tabScope) bool {
	switch {
	case s.level == scopeNone:
		return f.level != scopeMachine // an empty tab waiting for a pick
	case f.level == scopeMachine || s.level == scopeMachine:
		return false
	case f.level != s.level || f.machine != s.machine || f.project != s.project:
		return false
	}
	return f.section == 0 || f.section == s.section
}

// tabFilter is the group whose tabs are listed: the active tab's while
// typing in it, else the tree selection's. ok is false when nothing is
// selected (every tab is listed).
func (m *Model) tabFilter() (tabScope, bool) {
	if m.focus == focusMain && !m.previewing && len(m.tabs) > 0 {
		if s := m.tabScopeOf(m.tabs[m.activeTab]); s.level != scopeNone {
			return s, true
		}
	}
	r, ok := m.selectedRow()
	if !ok {
		return tabScope{}, false
	}
	return m.rowScope(r), true
}

// scopeName names a group for the tab list, e.g. "conch · agents".
func (m Model) scopeName(s tabScope) string {
	name := ""
	switch s.level {
	case scopeProject:
		if p := m.project(s.machine, s.project); p != nil {
			name = p.Name
		}
	case scopeCLI:
		name = "CLI"
	default:
		return ""
	}
	if len(m.machines) > 1 {
		if mach := m.machine(s.machine); mach != nil {
			name = mach.label + " › " + name
		}
	}
	switch s.section {
	case kindAgents:
		name += " · agents"
	case kindTerminals:
		name += " · terminals"
	}
	return name
}

func tabShows(t *tab, rowID string) bool {
	for _, l := range t.root.leaves() {
		if l.view.Row == rowID {
			return true
		}
	}
	return false
}

// visibleTabs lists the indexes of the tabs in the tab bar. The active tab
// is always listed, and so is a tab showing the selected row.
func (m *Model) visibleTabs() []int {
	f, ok := m.tabFilter()
	var out []int
	for i, t := range m.tabs {
		if !ok || inScope(f, m.tabScopeOf(t)) || (i == m.activeTab && !m.previewing) ||
			(m.cursor != "" && f.level != scopeMachine && tabShows(t, m.cursor)) {
			out = append(out, i)
		}
	}
	return out
}

// promote makes the preview a real tab, once it is split, typed into or
// opened.
func (m *Model) promote() {
	if !m.previewing {
		return
	}
	m.previewing = false
	pv := m.preview
	m.preview = nil
	if pv == nil || m.tabScopeOf(pv).level == scopeNone && len(m.tabs) > 0 {
		m.activeTab = clamp(m.activeTab, 0, len(m.tabs)-1)
		return
	}
	pv.home = m.tabScopeOf(pv)
	m.tabs = append(m.tabs, pv)
	m.activeTab = len(m.tabs) - 1
}

// pickTab chooses what the main area shows as the tree's cursor moves: the
// tab showing the selected row, else the active tab if it's still listed,
// else the tab last used in this group, else its first tab, else a preview
// of the row.
func (m *Model) pickTab() {
	// Only a cursor move picks: redraws and events keep the tab on screen.
	if m.keepTab || m.cursor == m.pickedFor {
		m.keepTab, m.pickedFor = false, m.cursor
		return
	}
	m.pickedFor = m.cursor
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	f := m.rowScope(r)
	if r.kind != kindMachine {
		if !m.previewing && len(m.tabs) > 0 && tabShows(m.tabs[m.activeTab], r.id) {
			return
		}
		for i, t := range m.tabs {
			if tabShows(t, r.id) {
				m.activeTab, m.previewing = i, false
				return
			}
		}
	}
	var vis []int
	for i, t := range m.tabs {
		if inScope(f, m.tabScopeOf(t)) {
			vis = append(vis, i)
		}
	}
	if pageRow(r.kind) {
		// A page (a project, its branches, a branch's changes, sessions)
		// shows itself: in the group's browsing tab, else as a preview. The
		// bar still lists the group's tabs.
		for _, i := range slices.Backward(vis) {
			if ls := m.tabs[i].root.leaves(); len(ls) == 1 && browsing(ls[0]) && !ls[0].pick {
				m.activeTab, m.previewing = i, false
				return // syncView puts the row in it
			}
		}
		m.showPreview(r)
		return
	}
	if !m.previewing && slices.Contains(vis, m.activeTab) {
		return
	}
	if i := slices.Index(m.tabs, m.scopeTab[f.key()]); i >= 0 && slices.Contains(vis, i) {
		m.activeTab, m.previewing = i, false
		return
	}
	if len(vis) > 0 {
		m.activeTab, m.previewing = vis[0], false
		return
	}
	m.showPreview(r)
}

// pageRow reports whether a row is a page to look at rather than a group
// of tabs.
func pageRow(k nodeKind) bool {
	switch k {
	case kindProject, kindBranches, kindBranch, kindMore, kindSessions:
		return true
	}
	return false
}

// showPreview shows a row in the preview.
func (m *Model) showPreview(r row) {
	m.previewing = true
	pv := m.tab()
	if len(pv.root.leaves()) > 1 {
		pv.root = &layoutNode{leaf: m.newLeaf(viewRef{})}
	}
	pv.focus = pv.root.leaf.id
	m.assign(pv.root.leaf, r)
}
