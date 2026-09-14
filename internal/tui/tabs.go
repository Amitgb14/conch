package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// paneKey identifies a pane across machines.
func paneKey(mid, id string) string { return mid + "|" + id }

func (m *Model) newLeaf(v viewRef) *leaf {
	m.leafSeq++
	return &leaf{id: m.leafSeq, view: v}
}

func (m *Model) tab() *tab {
	if len(m.tabs) == 0 {
		m.tabs = []*tab{{root: &layoutNode{leaf: m.newLeaf(viewRef{})}}}
		m.tabs[0].focus = m.tabs[0].root.leaf.id
	}
	m.activeTab = clamp(m.activeTab, 0, len(m.tabs)-1)
	return m.tabs[m.activeTab]
}

// mainRect is the screen area right of the sidebar, above the status bar.
func (m Model) mainRect() rect {
	if m.zoom {
		return rect{0, 0, m.width, m.height - statusHeight}
	}
	return rect{m.sidebarW, 0, m.width - m.sidebarW, m.height - statusHeight}
}

// leafRects lays out the active tab below its tab bar. Zoomed, the focused
// leaf fills the screen on its own.
func (m *Model) leafRects() (map[int]rect, []splitBar) {
	t := m.tab()
	rects := map[int]rect{}
	mr := m.mainRect()
	if m.zoom {
		rects[t.focused().id] = mr
		return rects, nil
	}
	var bars []splitBar
	t.root.layout(rect{mr.x, mr.y + 1, mr.w, mr.h - 1}, rects, &bars)
	return rects, bars
}

// inner is a leaf's content area inside its border (no border when zoomed).
func (m Model) inner(r rect) rect {
	if m.zoom {
		return r
	}
	return rect{r.x + 1, r.y + 1, max(r.w-2, 1), max(r.h-2, 1)}
}

func (m *Model) focusedRect() rect {
	rects, _ := m.leafRects()
	return m.inner(rects[m.tab().focused().id])
}

// paneArea is the size of the focused leaf's content.
func (m Model) paneArea() (cols, rows int) {
	if m.width == 0 {
		return 80, 24
	}
	r := m.focusedRect()
	return r.w, r.h
}

// mainOrigin is the screen position of the focused leaf's content.
func (m Model) mainOrigin() (x, y int) {
	r := m.focusedRect()
	return r.x, r.y
}

// syncView makes the screen match the layout: a single-leaf tab follows
// the tree's cursor; every pane shown in the tab is subscribed and sized to
// its leaf; branch leaves load their changes.
func (m *Model) syncView() tea.Cmd {
	t := m.tab()
	leaves := t.root.leaves()
	if r, ok := m.selectedRow(); ok && len(leaves) == 1 && !m.zoom && !leaves[0].pick && !m.shownElsewhere(r.id, leaves[0]) {
		m.assign(leaves[0], r)
	}

	rects, _ := m.leafRects()
	want := map[string]bool{}
	for _, l := range leaves {
		r, shown := rects[l.id]
		v := l.view
		mach := m.machine(v.Machine)
		if !shown || v.Kind != kindPane || mach == nil || mach.c == nil {
			continue
		}
		key := paneKey(v.Machine, v.PaneID)
		want[key] = true
		if p := mach.pane(v.PaneID); p != nil && p.State == proto.PaneRunning && m.width > 0 {
			in := m.inner(r)
			if mach.sizes[v.PaneID] != [2]int{in.w, in.h} {
				mach.sizes[v.PaneID] = [2]int{in.w, in.h}
				mach.c.Notify(proto.MethodPaneResize, proto.PaneResizeParams{ID: v.PaneID, Cols: in.w, Rows: in.h})
			}
		}
		if !m.subscribed[key] {
			m.subscribed[key] = true
			mach.c.Notify(proto.MethodPaneSubscribe, proto.PaneRef{ID: v.PaneID})
		}
	}
	for key := range m.subscribed {
		if want[key] {
			continue
		}
		delete(m.subscribed, key)
		delete(m.frames, key)
		mid, id, _ := strings.Cut(key, "|")
		if mach := m.machine(mid); mach != nil && mach.c != nil {
			mach.c.Notify(proto.MethodPaneUnsubscribe, proto.PaneRef{ID: id})
		}
	}

	// The focused leaf's pane is what keys, scrolling and selection act on.
	f := t.focused()
	mid, id := "", ""
	if f.view.Kind == kindPane && want[paneKey(f.view.Machine, f.view.PaneID)] {
		mid, id = f.view.Machine, f.view.PaneID
	}
	if mid != m.viewMachine || id != m.viewing {
		m.viewMachine, m.viewing = mid, id
		m.scrollMode, m.sel = false, nil
	}
	m.frame = m.frames[paneKey(mid, id)]
	m.offset = 0
	if m.frame != nil {
		m.offset = m.frame.Offset
	}

	var cmds []tea.Cmd
	for _, l := range leaves {
		v := l.view
		if v.Kind != kindBranch {
			l.changes = nil
			continue
		}
		if l.changes == nil || l.changes.machine != v.Machine || l.changes.projectID != v.ProjectID || l.changes.branch != v.Branch {
			l.changes = &changesView{machine: v.Machine, projectID: v.ProjectID, branch: v.Branch}
			cmds = append(cmds, l.changes.reload(m))
		}
	}
	m.changes = f.changes
	cmds = append(cmds, m.pollChanges())
	m.sessionsView = nil
	for _, l := range leaves {
		v := l.view
		switch v.Kind {
		case kindSessions:
			if l.sessions == nil || l.sessions.machine != v.Machine || l.sessions.projectID != v.ProjectID {
				l.sessions = &sessionsView{machine: v.Machine, projectID: v.ProjectID}
			}
			cmds = append(cmds, m.loadSessions(v.Machine, v.ProjectID, false))
		case kindProject:
			l.sessions = nil
			cmds = append(cmds, m.loadSessions(v.Machine, v.ProjectID, false))
		default:
			l.sessions = nil
		}
	}
	if f.view.Kind == kindSessions {
		m.sessionsView = f.sessions
	}
	return tea.Batch(cmds...)
}

// shownElsewhere reports whether a pane row is on screen in a leaf other
// than except, in any tab: a pane lives in one place, so moving the tree's
// cursor over it must not copy it into the current tab.
func (m *Model) shownElsewhere(rowID string, except *leaf) bool {
	for _, t := range m.tabs {
		for _, l := range t.root.leaves() {
			if l != except && l.view.Row == rowID && l.view.Kind == kindPane {
				return true
			}
		}
	}
	return false
}

// assign shows a tree row in a leaf.
func (m *Model) assign(l *leaf, r row) {
	l.pick = false
	if l.view.Row != r.id {
		l.view = viewOf(r)
		l.changes = nil
	}
}

// show puts a tree row on screen: focusing a leaf of the active tab that
// already shows it, else switching to a tab that does, else showing it in
// the focused leaf.
func (m *Model) show(r row) tea.Cmd {
	t := m.tab()
	for _, l := range t.root.leaves() {
		if l.view.Row == r.id {
			t.focus = l.id
			return m.syncView()
		}
	}
	for i, other := range m.tabs {
		for _, l := range other.root.leaves() {
			if i != m.activeTab && l.view.Row == r.id && r.kind == kindPane {
				m.activeTab, other.focus = i, l.id
				return m.syncView()
			}
		}
	}
	// Never replace a view the user arranged: fill an empty leaf, preview in
	// a single-leaf tab, else open a tab for it.
	leaves := t.root.leaves()
	f := t.focused()
	switch {
	case f.pick || f.view.empty():
		m.assign(f, r)
	case len(leaves) == 1:
		m.assign(f, r)
	default:
		for _, l := range leaves {
			if l.pick || l.view.empty() {
				t.focus = l.id
				m.assign(l, r)
				return m.syncView()
			}
		}
		return m.newTab(viewOf(r))
	}
	return m.syncView()
}

// focusLeaf moves focus to a leaf and the tree cursor to what it shows.
func (m *Model) focusLeaf(id int) tea.Cmd {
	t := m.tab()
	if t.leaf(id) == nil {
		return nil
	}
	t.focus = id
	if row := t.focused().view.Row; row != "" && indexOfRow(m.rows, row) >= 0 {
		m.cursor = row
		m.keepCursorVisible()
	}
	return m.syncView()
}

// split divides the focused leaf; the new half shows v and takes focus.
// Splitting without a view, or onto what the focused leaf already shows,
// doesn't mirror it (typing would land in both halves): a terminal pane
// gets a new shell beside it in the same directory, anything else an empty
// half to pick something for.
func (m *Model) split(dir splitDir, v viewRef) tea.Cmd {
	t := m.tab()
	f := t.focused()
	newShell := false
	if v.empty() || v.Row == f.view.Row {
		p := m.pane(f.view.Machine, f.view.PaneID)
		newShell = f.view.Kind == kindPane && p != nil && p.State == proto.PaneRunning
		v = viewRef{}
	}
	nl := m.newLeaf(v)
	nl.pick = v.empty()
	t.root.split(f.id, dir, nl)
	t.focus = nl.id
	m.zoom = false
	cmds := []tea.Cmd{m.syncView(), m.saveState()}
	if newShell {
		m.cursor = f.view.Row                // the new shell starts where the split pane runs
		cmds = append(cmds, m.openAgent("")) // shown in the new half when it starts
	}
	return tea.Batch(cmds...)
}

// closeLeaf removes the focused leaf (the pane keeps running). The last
// leaf of a tab closes the tab, unless it is the only tab.
func (m *Model) closeLeaf() tea.Cmd {
	t := m.tab()
	if len(t.root.leaves()) == 1 {
		if len(m.tabs) > 1 {
			return m.closeTab(m.activeTab)
		}
		t.root.leaf.view = viewRef{}
		return tea.Batch(m.syncView(), m.saveState())
	}
	t.root = t.root.remove(t.focus)
	t.focused()
	return tea.Batch(m.focusLeaf(t.focus), m.saveState())
}

// newTab opens a tab showing v. Without a view it doesn't copy what is
// selected: beside a terminal pane it starts a new shell in that directory,
// otherwise it waits for a row to be opened.
func (m *Model) newTab(v viewRef) tea.Cmd {
	var shellFrom *leaf
	if v.empty() {
		f := m.tab().focused()
		if p := m.pane(f.view.Machine, f.view.PaneID); f.view.Kind == kindPane && p != nil && p.State == proto.PaneRunning {
			shellFrom = f
		}
	}
	l := m.newLeaf(v)
	l.pick = v.empty()
	m.tabs = append(m.tabs, &tab{root: &layoutNode{leaf: l}, focus: l.id})
	m.activeTab = len(m.tabs) - 1
	m.zoom = false
	cmds := []tea.Cmd{m.focusLeaf(l.id), m.saveState()}
	if shellFrom != nil {
		m.cursor = shellFrom.view.Row
		cmds = append(cmds, m.openAgent(""))
	}
	return tea.Batch(cmds...)
}

func (m *Model) closeTab(i int) tea.Cmd {
	if len(m.tabs) <= 1 || i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	if m.activeTab >= i {
		m.activeTab = max(m.activeTab-1, 0)
	}
	return tea.Batch(m.focusLeaf(m.tab().focus), m.saveState())
}

func (m *Model) gotoTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.activeTab = i
	return tea.Batch(m.focusLeaf(m.tab().focus), m.saveState())
}

// moveFocus focuses the leaf next to the focused one.
func (m *Model) moveFocus(dx, dy int) tea.Cmd {
	rects, _ := m.leafRects()
	if id, ok := neighbor(rects, m.tab().focus, dx, dy); ok {
		return m.focusLeaf(id)
	}
	return nil
}

// ---- tab bar ----

// tabLabel names a tab: its custom name, else what its focused leaf shows.
func (m Model) tabLabel(t *tab) string {
	if t.name != "" {
		return t.name
	}
	// Named after its first view, not the focused one, so a tab keeps its
	// name while focus moves between its splits.
	leaves := t.root.leaves()
	v := leaves[0].view
	if v.empty() {
		v = t.focused().view
	}
	label := "empty"
	switch v.Kind {
	case kindPane:
		if p := m.pane(v.Machine, v.PaneID); p != nil {
			label = p.DisplayName()
		}
	case kindBranch:
		label = v.Branch
	case kindProject, kindBranches, kindAgents, kindTerminals, kindMore, kindSessions:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			label = proj.Name
		}
	case kindMachine:
		if mach := m.machine(v.Machine); mach != nil {
			label = mach.label
		}
	}
	if v.empty() {
		label = "empty"
	}
	if n := len(t.root.leaves()); n > 1 {
		label += " ⊞"
	}
	return ansi.Truncate(label, 20, "…")
}

type tabHit struct {
	x0, x1 int
	tab    int // -1: new tab button; -2: close active tab
}

// tabBar renders the tab bar across the main area and where each tab is.
func (m Model) tabBar(w int) (string, []tabHit) {
	var b strings.Builder
	var hits []tabHit
	x := 0
	put := func(s string, tab int) {
		sw := ansi.StringWidth(s)
		if x+sw > w {
			return
		}
		b.WriteString(s)
		hits = append(hits, tabHit{x0: x, x1: x + sw, tab: tab})
		x += sw
	}
	for i, t := range m.tabs {
		label := " " + itoa(i+1) + " " + m.tabLabel(t) + " "
		if i == m.activeTab {
			put(styleSel.Render(label), i)
			if len(m.tabs) > 1 {
				put(styleSel.Render("× "), -2)
			}
		} else {
			put(styleMuted.Render(label), i)
		}
		b.WriteString(" ")
		x++
	}
	put(styleAccent.Render(" + "), -1)
	return fit(b.String(), w), hits
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// ---- persistence ----

func (m Model) savedTabs() []savedTab {
	var out []savedTab
	for _, t := range m.tabs {
		out = append(out, savedTab{Name: t.name, Root: saveNode(t.root, t.focus)})
	}
	return out
}

func (m *Model) restoreTabs(saved []savedTab, active int) {
	for _, st := range saved {
		root, focus, err := loadNode(st.Root, func() int { m.leafSeq++; return m.leafSeq })
		if err != nil {
			continue
		}
		t := &tab{name: st.Name, root: root, focus: focus}
		t.focused()
		m.tabs = append(m.tabs, t)
	}
	m.activeTab = active
	m.tab()
}

// dropPane takes a closed pane out of the layout, as tmux does: its split
// closes, and a tab that showed only it closes too (unless it is the last).
func (m *Model) dropPane(mid, id string) {
	row := paneNodeID(mid, id)
	for i := 0; i < len(m.tabs); i++ {
		t := m.tabs[i]
		for _, l := range t.root.leaves() {
			if l.view.Row != row {
				continue
			}
			switch {
			case len(t.root.leaves()) > 1:
				t.root = t.root.remove(l.id)
				if t.leaf(t.focus) == nil {
					t.focus = t.root.leaves()[0].id
				}
			case len(m.tabs) > 1:
				m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
				if m.activeTab > i || m.activeTab >= len(m.tabs) {
					m.activeTab = max(m.activeTab-1, 0)
				}
				i--
			default:
				l.view = viewRef{}
			}
			break
		}
	}
}
