package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

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
	if m.previewing || len(m.tabs) == 0 {
		if m.preview == nil {
			l := m.newLeaf(viewRef{})
			m.preview = &tab{root: &layoutNode{leaf: l}, focus: l.id}
		}
		m.previewing = true
		return m.preview
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
	if m.focus != focusMain && !m.zoom {
		m.pickTab()
	} else { // what's on screen was chosen here: the cursor follows it
		m.keepTab, m.pickedFor = false, m.cursor
	}
	t := m.tab()
	leaves := t.root.leaves()
	// A browsing tab follows the tree's cursor over pages; agents and
	// terminals open (in their own tab) only when activated.
	if r, ok := m.selectedRow(); ok && len(leaves) == 1 && !m.zoom && !leaves[0].pick &&
		r.kind != kindPane && (r.kind != kindMachine || m.previewing) && browsing(leaves[0]) && !ownsTab(leaves[0].view) {
		m.assign(leaves[0], r)
	}
	if f, ok := m.tabFilter(); ok && !m.previewing && m.focus != focusMain {
		if m.scopeTab == nil {
			m.scopeTab = map[string]*tab{}
		}
		m.scopeTab[f.key()] = t
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
	if t.seen != f.id { // remember the previous split and tab for ; and l
		if t.leaf(t.seen) != nil {
			t.last = t.seen
		}
		t.seen = f.id
	}
	switched := m.seenTab != t
	if switched {
		if slices.Contains(m.tabs, m.seenTab) {
			m.lastTab = m.seenTab
		}
		m.seenTab = t
	}
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
		switch {
		case l.changes == nil || l.changes.machine != v.Machine || l.changes.projectID != v.ProjectID || l.changes.branch != v.Branch:
			cv, known := m.changesFor(v.Machine, v.ProjectID, v.Branch)
			l.changes = cv
			if known {
				cmds = append(cmds, cv.poll(m)) // what was read before shows at once; refresh behind it
			} else {
				cmds = append(cmds, cv.reload(m))
			}
		case switched:
			// Only the tab on screen is polled, and worktree events are
			// applied to it alone, so a tab coming back may be holding
			// something old. Read it again behind what it already shows.
			cmds = append(cmds, l.changes.poll(m), l.changes.liveDiff(m))
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
	m.queueView = nil
	for _, l := range leaves {
		if l.view.Kind == kindReviewQueue {
			if l.queue == nil {
				l.queue = &queueView{}
			}
		} else {
			l.queue = nil
		}
	}
	if f.view.Kind == kindReviewQueue {
		m.queueView = f.queue
	}
	return tea.Batch(cmds...)
}

// openTabs are the tabs plus the preview, which is where a row the tree is
// only browsing — a branch's changes, say — is shown. Answers from the
// server have to reach it too, or it waits for a reply it was sent.
func (m *Model) openTabs() []*tab {
	if m.preview == nil {
		return m.tabs
	}
	return append(append(make([]*tab, 0, len(m.tabs)+1), m.tabs...), m.preview)
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

// show puts a tree row on screen. What is already on screen is focused where
// it is. Agents and terminals get a tab each, as tmux windows do: an empty
// split or browsing tab takes one in, else it opens its own tab. Other rows
// (projects, branches, sessions) share a browsing tab.
func (m *Model) show(r row) tea.Cmd {
	// Where we were, so ctrl+b b can go back to it: opening a check's
	// terminal from the queue, say, and then returning to the list.
	if was := m.tab().focused().view; !was.empty() && was.Row != r.id {
		m.prevView = was
	}
	m.keepTab = true
	if m.tab(); m.previewing { // the preview shows the row, or is about to: keep it as a tab
		// Unless a tab already holds it: a branch clicked on the Branches
		// page, which is itself a preview, opened a second tab for it.
		if i, l := m.tabShowing(r.id); l != nil {
			m.activeTab, m.tabs[i].focus, m.previewing = i, l.id, false
			return m.syncView()
		}
		pv := m.tab()
		if !tabShows(pv, r.id) {
			m.assign(pv.focused(), r)
		}
		if r.kind == kindPane {
			m.removeRowExcept(r.id, pv)
		}
		m.promote()
		return tea.Batch(m.syncView(), m.saveState())
	}
	t := m.tab()
	for _, l := range t.root.leaves() {
		if l.view.Row == r.id {
			t.focus = l.id
			return m.syncView()
		}
	}
	for i, other := range m.tabs {
		for _, l := range other.root.leaves() {
			if i != m.activeTab && l.view.Row == r.id && (r.kind == kindPane || ownsTab(l.view)) {
				m.activeTab, other.focus = i, l.id
				return m.syncView()
			}
		}
	}
	leaves := t.root.leaves()
	f := t.focused()
	for _, l := range leaves { // an empty split waiting for something
		if l.pick || l.view.empty() {
			t.focus = l.id
			m.assign(l, r)
			return m.syncView()
		}
	}
	scope := m.rowScope(r)
	if len(leaves) == 1 && browsing(f) && !ownsTab(f.view) && !ownsTab(viewOf(r)) && inScope(scope, m.tabScopeOf(t)) {
		m.assign(f, r) // the browsing tab in view
		return m.syncView()
	}
	if r.kind != kindPane && !ownsTab(viewOf(r)) {
		for i := len(m.tabs) - 1; i >= 0; i-- { // the group's latest browsing tab
			if ls := m.tabs[i].root.leaves(); len(ls) == 1 && browsing(ls[0]) && !ownsTab(ls[0].view) && inScope(scope, m.tabScopeOf(m.tabs[i])) {
				m.activeTab, m.tabs[i].focus = i, ls[0].id
				m.assign(ls[0], r)
				return tea.Batch(m.syncView(), m.saveState())
			}
		}
	}
	return m.newTab(viewOf(r))
}

// browsing reports whether a leaf shows something other than a pane: a
// page it's fine to replace by opening another row.
func browsing(l *leaf) bool {
	return l.pick || l.view.empty() || l.view.Kind != kindPane
}

// tabShowing finds the tab and leaf already showing a row, or nil.
func (m *Model) tabShowing(rowID string) (int, *leaf) {
	for i, t := range m.tabs {
		for _, l := range t.root.leaves() {
			if l.view.Row == rowID {
				return i, l
			}
		}
	}
	return -1, nil
}

// ownsTab reports whether a view keeps the tab it is in: a branch does, so
// each branch stays open in its own tab while you look at another. Other
// pages (a project, its sessions) share the group's browsing tab.
// ownsTab reports whether a view keeps a tab to itself rather than sharing
// the browsing tab. A branch does, and so does the review queue: opening a
// row from it must leave the queue where it was, not replace it.
func ownsTab(v viewRef) bool { return v.Kind == kindBranch || v.Kind == kindReviewQueue }

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
	// Browsing the tree shows the row in a preview rather than in a tab, and
	// splitting from there means "put this beside what I was looking at". So
	// the tab we were on is the one that divides: promoting the preview
	// would split the row away from itself and leave the new half empty.
	if m.previewing && !v.empty() && len(m.tabs) > 0 {
		if pv := m.tab(); pv.focused().view.Row == v.Row {
			m.previewing, m.preview = false, nil
			m.activeTab = clamp(m.activeTab, 0, len(m.tabs)-1)
		}
	}
	m.promote() // a preview of something else still becomes the tab to divide
	m.keepTab = true
	t := m.tab()
	f := t.focused()
	newShell := false
	if v.empty() || v.Row == f.view.Row {
		p := m.pane(f.view.Machine, f.view.PaneID)
		newShell = f.view.Kind == kindPane && p != nil && p.State == proto.PaneRunning
		v = viewRef{}
	} else if v.Kind == kindPane {
		m.removeRow(v.Row) // moved here, as tmux's join-pane: never shown twice
		t = m.tab()
	}
	nl := m.newLeaf(v)
	nl.pick = v.empty()
	t.root.split(f.id, dir, nl)
	t.focus = nl.id
	m.zoom = false
	cmds := []tea.Cmd{m.syncView(), m.saveState()}
	if newShell {
		m.cursor = f.view.Row                // the new shell starts where the split pane runs
		nl.await = true                      // and the half goes if it never starts
		cmds = append(cmds, m.openAgent("")) // shown in the new half when it starts
	}
	return tea.Batch(cmds...)
}

// closeLeaf removes the focused leaf (the pane keeps running). The last
// leaf of a tab closes the tab, unless it is the only tab.
func (m *Model) closeLeaf() tea.Cmd {
	t := m.tab()
	if len(t.root.leaves()) == 1 {
		if m.previewing {
			return nil
		}
		return m.closeTab(m.activeTab)
	}
	t.root = t.root.remove(t.focus)
	t.focused()
	return tea.Batch(m.focusLeaf(t.focus), m.saveState())
}

// newShellTab opens a tab with a terminal in it, wherever the tree is
// pointing: ctrl+b c always gives you a shell, as tmux's new window does.
// A tab that waited to be filled instead piled up unseen — the tab bar lists
// only the tabs of the group the tree has selected — and half a dozen empty
// ones appeared at once the next time a pane brought that group on screen.
func (m *Model) newShellTab() tea.Cmd {
	cmd := m.newTab(viewRef{})
	l := m.tab().focused()
	if !l.view.empty() || l.await {
		return cmd // already showing something, or a shell is on its way
	}
	l.await = true
	return tea.Batch(cmd, m.openAgent(""))
}

// dropAwaiting removes a leaf that was opened for a pane which never
// started, closing its tab or split, and returns whether one went. Only the
// newest is dropped: one failure, one leaf.
func (m *Model) dropAwaiting() bool {
	for i := len(m.tabs) - 1; i >= 0; i-- {
		t := m.tabs[i]
		leaves := t.root.leaves()
		for j := len(leaves) - 1; j >= 0; j-- {
			l := leaves[j]
			if !l.await || !l.view.empty() {
				continue
			}
			if len(leaves) == 1 {
				if len(m.tabs) == 1 {
					l.await = false // the only tab stays, empty
					return false
				}
				m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
				if m.activeTab >= i {
					m.activeTab = max(m.activeTab-1, 0)
				}
				return true
			}
			t.root = t.root.remove(l.id)
			if t.leaf(t.focus) == nil {
				t.focus = t.root.leaves()[0].id
			}
			return true
		}
	}
	return false
}

// arrived clears the waiting mark once a pane is on screen.
func (m *Model) arrived() {
	for _, t := range m.tabs {
		for _, l := range t.root.leaves() {
			if !l.view.empty() {
				l.await = false
			}
		}
	}
}

// changesTab opens the changes of the branch the focused split is on, in a
// tab of their own: an agent's pane and the diff it is writing, without
// walking the tree to find the branch. A tab already showing them is gone
// to rather than opened twice.
func (m *Model) changesTab() tea.Cmd {
	v := m.tab().focused().view
	mid, pid, branch := v.Machine, v.ProjectID, v.Branch
	if v.Kind == kindPane { // a pane knows the worktree it was started in
		if p := m.pane(v.Machine, v.PaneID); p != nil {
			pid, branch = p.ProjectID, p.Branch
		}
	}
	if mid == "" || pid == "" || branch == "" {
		m.setFlash("this split is not on a branch", true)
		return nil
	}
	row := branchNodeID(mid, pid, branch)
	if i, l := m.tabShowing(row); l != nil {
		m.tabs[i].focus = l.id
		return m.gotoTab(i)
	}
	return m.newTab(viewRef{Row: row, Kind: kindBranch, Machine: mid, ProjectID: pid, Branch: branch})
}

// newTab opens a tab showing v. Without a view it doesn't copy what is
// selected: beside a terminal pane it starts a new shell in that directory,
// otherwise it waits for a row to be opened.
func (m *Model) newTab(v viewRef) tea.Cmd {
	if v.Kind == kindPane && !v.empty() {
		// A pane already on screen moves, as tmux's break-pane: alone in its
		// tab it already has one.
		for i, t := range m.tabs {
			if ls := t.root.leaves(); len(ls) == 1 && ls[0].view.Row == v.Row {
				return m.gotoTab(i)
			}
		}
		m.removeRow(v.Row)
	}
	home, _ := m.tabFilter()
	if home.level == scopeMachine {
		home = tabScope{}
	}
	var shellFrom *leaf
	if v.empty() {
		f := m.tab().focused()
		if p := m.pane(f.view.Machine, f.view.PaneID); f.view.Kind == kindPane && p != nil && p.State == proto.PaneRunning {
			shellFrom = f
		}
	}
	l := m.newLeaf(v)
	l.pick = v.empty()
	m.tabs = append(m.tabs, &tab{root: &layoutNode{leaf: l}, focus: l.id, home: home})
	m.activeTab, m.previewing, m.keepTab = len(m.tabs)-1, false, true
	m.zoom = false
	cmds := []tea.Cmd{m.focusLeaf(l.id), m.saveState()}
	if shellFrom != nil {
		m.cursor = shellFrom.view.Row
		l.await = true
		cmds = append(cmds, m.openAgent(""))
	}
	return tea.Batch(cmds...)
}

func (m *Model) closeTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	if m.activeTab >= i {
		m.activeTab = max(m.activeTab-1, 0)
	}
	if len(m.tabs) == 0 {
		return tea.Batch(m.syncView(), m.saveState())
	}
	return tea.Batch(m.focusLeaf(m.tab().focus), m.saveState())
}

// stepTab goes to the next (d = 1) or previous (d = -1) tab in the bar.
func (m *Model) stepTab(d int) tea.Cmd {
	vis := m.visibleTabs()
	if len(vis) == 0 {
		return nil
	}
	pos := slices.Index(vis, m.activeTab)
	switch {
	case m.previewing || pos < 0:
		pos = 0
		if d < 0 {
			pos = len(vis) - 1
		}
	default:
		pos = (pos + d + len(vis)) % len(vis)
	}
	return m.switchTab(vis[pos])
}

// gotoVisibleTab goes to the n-th tab in the bar (0-based).
func (m *Model) gotoVisibleTab(n int) tea.Cmd {
	if vis := m.visibleTabs(); n < len(vis) {
		return m.switchTab(vis[n])
	}
	return nil
}

// switchTab goes to a tab listed in the bar. From the tree the cursor stays,
// so the list doesn't change under ctrl+b n and p; typing in a pane, keys go
// to the focused split, which the cursor follows.
func (m *Model) switchTab(i int) tea.Cmd {
	if m.focus == focusMain {
		return m.gotoTab(i)
	}
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.activeTab, m.previewing, m.keepTab = i, false, true
	return tea.Batch(m.syncView(), m.saveState())
}

func (m *Model) gotoTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.activeTab, m.previewing, m.keepTab = i, false, true
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

// lastSplit focuses the split focused before this one (tmux's last-pane).
func (m *Model) lastSplit() tea.Cmd {
	if t := m.tab(); t.last != t.focus && t.leaf(t.last) != nil {
		return m.focusLeaf(t.last)
	}
	m.setFlash("no previous split", true)
	return nil
}

// gotoLastTab switches to the tab active before this one (tmux's
// last-window).
func (m *Model) gotoLastTab() tea.Cmd {
	if i := slices.Index(m.tabs, m.lastTab); i >= 0 && i != m.activeTab {
		return m.gotoTab(i)
	}
	m.setFlash("no previous tab", true)
	return nil
}

// swapSplit trades the focused split's view with the previous (d = -1) or
// next (d = 1) split's, as tmux's swap-pane; focus stays with the view.
func (m *Model) swapSplit(d int) tea.Cmd {
	t := m.tab()
	ls := t.root.leaves()
	if len(ls) < 2 {
		return nil
	}
	i := slices.IndexFunc(ls, func(l *leaf) bool { return l.id == t.focus })
	if i < 0 {
		return nil
	}
	a, b := ls[i], ls[(i+d+len(ls))%len(ls)]
	a.view, b.view = b.view, a.view
	a.changes, b.changes = b.changes, a.changes
	a.sessions, b.sessions = b.sessions, a.sessions
	a.pick, b.pick = b.pick, a.pick
	t.focus = b.id
	return tea.Batch(m.syncView(), m.saveState())
}

// toggleSync turns typing into every split of the tab on or off.
func (m *Model) toggleSync() tea.Cmd {
	t := m.tab()
	if !t.sync && len(m.syncedPanes(t)) < 2 {
		m.setFlash("split the tab first: sync types into every agent and terminal in it", true)
		return nil
	}
	t.sync = !t.sync
	if t.sync {
		m.setFlash(fmt.Sprintf("typing goes to all %d splits · %s S to stop", len(m.syncedPanes(t)), m.cfg.Keys.Prefix), false)
	} else {
		m.setFlash("typing goes to the focused split only", false)
	}
	return m.saveState()
}

// syncedPanes lists the running panes shown in a tab's splits.
func (m *Model) syncedPanes(t *tab) []viewRef {
	var out []viewRef
	for _, l := range t.root.leaves() {
		if p := m.pane(l.view.Machine, l.view.PaneID); l.view.Kind == kindPane && p != nil && p.State == proto.PaneRunning {
			out = append(out, l.view)
		}
	}
	return out
}

// forwardSynced sends a key typed in pane mid/id to the tab's other panes
// when the tab is synchronized.
func (m *Model) forwardSynced(mid, id string, k tea.KeyMsg) {
	t := m.tab()
	if !t.sync {
		return
	}
	for _, v := range m.syncedPanes(t) {
		if v.Machine == mid && v.PaneID == id {
			continue
		}
		m.sendKey(v.Machine, v.PaneID, k)
	}
}

// repeatTime is how long resize keys keep working without the prefix, like
// tmux's repeat-time.
const repeatTime = 500 * time.Millisecond

// resizeKey maps ctrl+arrow (one cell) and alt+arrow (five) to a resize.
func resizeKey(key string) (dx, dy int, ok bool) {
	step := 1
	switch {
	case strings.HasPrefix(key, "alt+"):
		step = 5
	case !strings.HasPrefix(key, "ctrl+"):
		return 0, 0, false
	}
	_, arrow, _ := strings.Cut(key, "+")
	switch arrow {
	case "left":
		return -step, 0, true
	case "right":
		return step, 0, true
	case "up":
		return 0, -step, true
	case "down":
		return 0, step, true
	}
	return 0, 0, false
}

// resizeFocus moves the focused split's nearest border.
func (m *Model) resizeFocus(dx, dy int) tea.Cmd {
	m.repeatUntil = time.Now().Add(repeatTime)
	t := m.tab()
	_, bars := m.leafRects()
	dir, d := splitRight, dx
	if dy != 0 {
		dir, d = splitDown, dy
	}
	if !resize(t.root, bars, t.focus, dir, d) {
		return nil
	}
	return tea.Batch(m.syncView(), m.saveState())
}

// repeatResize handles a resize key pressed again soon after the last one,
// without the prefix.
func (m *Model) repeatResize(key string) (tea.Cmd, bool) {
	if time.Now().After(m.repeatUntil) {
		return nil, false
	}
	dx, dy, ok := resizeKey(key)
	if !ok {
		m.repeatUntil = time.Time{}
		return nil, false
	}
	return m.resizeFocus(dx, dy), true
}

// openTabPicker lists the tabs and their splits to jump to (tmux's
// choose-tree).
func (m *Model) openTabPicker() {
	var items []menuItem
	sel := 0
	// Every tab, grouped like the tree.
	order := make([]int, len(m.tabs))
	for i := range order {
		order[i] = i
	}
	rank := map[string]int{} // groups in the order their first tab appears
	for _, t := range m.tabs {
		if k := m.tabScopeOf(t).key(); rank[k] == 0 {
			rank[k] = len(rank) + 1
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return rank[m.tabScopeOf(m.tabs[a]).key()] - rank[m.tabScopeOf(m.tabs[b]).key()]
	})
	for _, i := range order {
		t := m.tabs[i]
		if i == m.activeTab && !m.previewing {
			sel = len(items)
		}
		label := m.tabLabel(t)
		if ls := t.root.leaves(); len(ls) == 1 && t.name == "" {
			label = m.pickerLabel(ls[0].view)
		}
		if name := m.scopeName(m.tabScopeOf(t)); name != "" {
			label += styleMuted.Render("  " + name)
		}
		items = append(items, menuItem{"", label, func(m *Model) tea.Cmd { return m.gotoTab(i) }})
		ls := t.root.leaves()
		if len(ls) < 2 {
			continue
		}
		for j, l := range ls {
			branch := "├ "
			if j == len(ls)-1 {
				branch = "└ "
			}
			if i == m.activeTab && l.id == t.focus {
				sel = len(items)
			}
			id := l.id
			items = append(items, menuItem{"", "  " + branch + m.pickerLabel(l.view), func(m *Model) tea.Cmd {
				if i >= len(m.tabs) || m.tabs[i] != t {
					return nil
				}
				m.activeTab, m.previewing, m.keepTab = i, false, true
				return tea.Batch(m.focusLeaf(id), m.saveState())
			}})
		}
	}
	mr := m.mainRect()
	m.overlay = &menu{title: "Tabs", items: items, sel: sel, x: mr.x + mr.w/2 - 20, y: mr.y + 2}
}

// ---- tab bar ----

// tabLabel names a tab: its custom name, else what its focused leaf shows.
func (m Model) tabLabel(t *tab) string {
	if t.name != "" {
		return t.name + m.tabAlert(t)
	}
	// Named after its first view, not the focused one, so a tab keeps its
	// name while focus moves between its splits.
	leaves := t.root.leaves()
	v := leaves[0].view
	if v.empty() {
		v = t.focused().view
	}
	label := m.viewLabel(v)
	if n := len(t.root.leaves()); n > 1 {
		label += " ⊞"
	}
	return ansi.Truncate(label, 20, "…") + m.tabAlert(t)
}

// tabAlert flags a tab holding a watched pane with something to tell, as
// tmux's window list does: ~ gone quiet, # printed.
func (m Model) tabAlert(t *tab) string {
	flag := ""
	for _, l := range t.root.leaves() {
		if l.view.Kind != kindPane {
			continue
		}
		switch p := m.pane(l.view.Machine, l.view.PaneID); {
		case p == nil:
		case p.Alert == proto.AlertSilence:
			return " ~" // it finished: says more than that it printed
		case p.Alert == proto.AlertActivity:
			flag = " #"
		}
	}
	return flag
}

// viewLabel names what a leaf shows.
func (m Model) viewLabel(v viewRef) string {
	label := "empty"
	switch v.Kind {
	case kindPane:
		if p := m.pane(v.Machine, v.PaneID); p != nil {
			label = p.DisplayName()
		}
	case kindBranch:
		label = v.Branch
	case kindReviewQueue:
		label = "queue"
	case kindProject, kindBranches, kindAgents, kindTerminals, kindSSH, kindMore, kindSessions:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			label = proj.Name
		}
	case kindMachine:
		if mach := m.machine(v.Machine); mach != nil {
			label = mach.label
		}
	case kindWorkspace:
		label = "Workspace"
	}
	if v.empty() {
		label = "empty"
	}
	return label
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
	for n, i := range m.visibleTabs() {
		t := m.tabs[i]
		label := " " + itoa(n+1) + " " + m.tabLabel(t) + " "
		if i == m.activeTab && !m.previewing {
			if x+ansi.StringWidth(label)+2 <= w { // the close button only beside its tab
				put(styleSel.Render(label), i)
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
		out = append(out, savedTab{Name: t.name, Root: saveNode(t.root, t.focus), Sync: t.sync})
	}
	return out
}

func (m *Model) restoreTabs(saved []savedTab, active int) {
	var activeT *tab
	for n, st := range saved {
		root, focus, err := loadNode(st.Root, func() int { m.leafSeq++; return m.leafSeq })
		if err != nil {
			continue
		}
		t := &tab{name: st.Name, root: root, focus: focus, sync: st.Sync}
		t.focused()
		shows := false
		for _, l := range t.root.leaves() {
			shows = shows || !l.view.empty() && l.view.Kind != kindMachine
		}
		if !shows { // machine pages and empty tabs are previews now
			continue
		}
		if n == active {
			activeT = t
		}
		m.tabs = append(m.tabs, t)
	}
	m.activeTab = max(slices.Index(m.tabs, activeT), 0)
	m.tab()
}

// dropPane takes a closed pane out of the layout, as tmux does: its split
// closes, and a tab that showed only it closes too (unless it is the last).
func (m *Model) dropPane(mid, id string) { m.removeRow(paneNodeID(mid, id)) }

// removeRow takes a view out of every split and tab showing it.
func (m *Model) removeRow(row string) { m.removeRowExcept(row, nil) }

// removeRowExcept takes a view out of every tab but except. A tab left
// with nothing closes.
func (m *Model) removeRowExcept(row string, except *tab) {
	if m.preview != nil && m.preview != except {
		for _, l := range m.preview.root.leaves() {
			if l.view.Row == row {
				l.view = viewRef{}
			}
		}
	}
	for i := 0; i < len(m.tabs); i++ {
		t := m.tabs[i]
		if t == except {
			continue
		}
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
			default:
				m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
				if m.activeTab > i || m.activeTab >= len(m.tabs) {
					m.activeTab = max(m.activeTab-1, 0)
				}
				i--
			}
			break
		}
	}
}

// closeSplitAsk closes the focused split. A running agent or terminal in it
// ends, after confirming, as tmux's kill-pane does; its view then leaves the
// layout when the server reports it closed.
func (m *Model) closeSplitAsk() tea.Cmd {
	f := m.tab().focused()
	p := m.pane(f.view.Machine, f.view.PaneID)
	if f.view.Kind != kindPane || p == nil {
		return m.closeLeaf()
	}
	mid, id := f.view.Machine, p.ID
	if p.State != proto.PaneRunning {
		return m.callOn(mid, proto.MethodPaneClose, proto.PaneRef{ID: id}, nil, nil)
	}
	m.overlay = newConfirm(fmt.Sprintf("Close %s? Its process stops.", p.DisplayName()), func(m *Model) tea.Cmd {
		return m.callOn(mid, proto.MethodPaneClose, proto.PaneRef{ID: id}, nil, nil)
	})
	return nil
}

// closeTabAsk closes a tab and, after confirming, ends the agents and
// terminals in it.
func (m *Model) closeTabAsk(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	type ref struct{ mid, id string }
	var running []ref
	var names []string
	for _, l := range m.tabs[i].root.leaves() {
		if p := m.pane(l.view.Machine, l.view.PaneID); l.view.Kind == kindPane && p != nil {
			running = append(running, ref{l.view.Machine, p.ID})
			if p.State == proto.PaneRunning {
				names = append(names, p.DisplayName())
			}
		}
	}
	closeAll := func(m *Model) tea.Cmd {
		var cmds []tea.Cmd
		for _, r := range running {
			cmds = append(cmds, m.callOn(r.mid, proto.MethodPaneClose, proto.PaneRef{ID: r.id}, nil, nil))
		}
		if len(m.tabs) > 1 && i < len(m.tabs) {
			cmds = append(cmds, m.closeTab(i))
		}
		return tea.Batch(cmds...)
	}
	if len(names) == 0 {
		return closeAll(m)
	}
	m.overlay = newConfirm(fmt.Sprintf("Close this tab? It ends %s.", strings.Join(names, ", ")), closeAll)
	return nil
}
