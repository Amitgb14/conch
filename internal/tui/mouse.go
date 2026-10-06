package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.overlay != nil {
		m.clearHover()
		return m, m.overlay.mouse(&m, msg, m.overlay.render(m))
	}
	// A pointer moving with nothing held is conch's own business: it lights
	// what is under it and goes no further, so a program in a pane still
	// sees only the mouse it asked for.
	if m.cfg.UI.Hover && msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonNone &&
		m.sel == nil && m.barDrag == nil && !m.dragging && !m.tabDrag && m.leafDrag == 0 && m.scrollDrag == 0 {
		m.hoverAt(msg)
		return m, nil
	}
	press := msg.Action == tea.MouseActionPress
	left := msg.Button == tea.MouseButtonLeft
	wheel := msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown

	// Resizing the sidebar by dragging its right edge.
	if m.dragging {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.sidebarW = clamp(msg.X+1, minSidebarWidth, min(maxSidebarWidth, m.width-20))
		case tea.MouseActionRelease:
			m.dragging = false
			return m, tea.Batch(m.rebuild(), m.saveState())
		}
		return m, nil
	}

	// Dragging a tab along the bar. Pressing one already made it active, so
	// the drag moves the active tab to whichever tab the pointer is over —
	// the bar reorders under the pointer rather than drawing a marker, and
	// the pointer then sits on the tab it moved, which is what stops it
	// swapping back and forth. The drag owns the mouse until it is let go,
	// wherever that happens.
	if m.tabDrag {
		switch msg.Action {
		case tea.MouseActionMotion:
			mr := m.mainRect()
			if msg.Y != mr.y {
				return m, nil // off the bar: nothing moves until it comes back
			}
			if pos := m.tabPosAt(msg.X - mr.x); pos >= 0 {
				return m, m.moveTabTo(pos)
			}
			return m, nil
		case tea.MouseActionRelease:
			m.tabDrag = false
			return m, nil
		}
		return m, nil
	}

	// Dragging a row of the tree onto a tab's section, where the tree is
	// grouped by tab: the same move as dropping a split on a tab, reached
	// from the tree. The drag owns the mouse until it is let go.
	if m.rowDrag != "" {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.rowDrop = ""
			if r, ok := m.rowAtY(msg.X, msg.Y); ok && (r.kind == kindTab || r.kind == kindFolder || m.sectionOf(r)) {
				m.rowDrop = r.id
			}
			return m, nil
		case tea.MouseActionRelease:
			from, to := m.rowDrag, m.rowDrop
			m.rowDrag, m.rowDrop = "", ""
			if to == "" {
				// A saved host is connected by a click, and a press on it
				// may have been the start of a drag: let go where it was
				// picked up, it was the click.
				if r, ok := m.rowAtY(msg.X, msg.Y); ok && r.id == from && r.kind == kindSavedSSH {
					return m, m.connectSSH(savedSSHTarget(r.id))
				}
				return m, nil // let go over nothing that takes a pane
			}
			return m, m.dropRowOnTab(from, to)
		}
		return m, nil
	}

	// Dragging the scrollbar on a pane's right border.
	if m.scrollDrag != 0 {
		switch msg.Action {
		case tea.MouseActionMotion:
			m.scrollTo(msg.Y)
			return m, nil
		case tea.MouseActionRelease:
			m.scrollDrag = 0
			return m, nil
		}
		return m, nil
	}

	// Dragging a split by its title onto another swaps the two, so a tree
	// of panes can be rearranged without closing anything. The layout
	// itself does not move: the halves stay the size they were given.
	if m.leafDrag != 0 {
		switch msg.Action {
		case tea.MouseActionRelease:
			from, to := m.leafDrag, 0
			tab := m.tabDrop
			m.leafDrag, m.leafDrop, m.tabDrop = 0, 0, tabDropNone
			// Let go on a tab: the split moves there instead of swapping.
			// On the +, it goes to a tab of its own.
			if mr := m.mainRect(); msg.Y == mr.y {
				switch x := msg.X - mr.x; {
				case tab >= 0 && tab == m.tabAt(x):
					return m, m.moveLeafToTab(from, tab)
				case tab == tabHitPlus && m.plusAt(x):
					return m, m.moveLeafToNewTab(from)
				}
				return m, nil // the bar, but neither a tab nor the +
			}
			rects, _ := m.leafRects()
			to = m.leafAt(rects, msg.X, msg.Y)
			if to == 0 || to == from || !m.tab().swapLeaves(from, to) {
				return m, nil // let go over nothing, or over itself
			}
			// Both halves are marked for a moment afterwards: a swap of two
			// panes full of text is otherwise hard to see happen at all.
			m.swapMark, m.swapUntil = [2]int{from, to}, time.Now().Add(swapMarkFor)
			return m, tea.Batch(m.focusLeaf(to), m.syncView(), m.saveState(),
				tea.Tick(swapMarkFor, func(time.Time) tea.Msg { return swapMarkExpiredMsg{} }))
		case tea.MouseActionMotion:
			// Over the bar, it is a tab it would move to, not a split it
			// would swap with; the tab under the pointer is marked.
			if mr := m.mainRect(); msg.Y == mr.y {
				x := msg.X - mr.x
				m.leafDrop = 0
				switch i := m.tabAt(x); {
				case i >= 0:
					m.tabDrop = i
				case m.plusAt(x):
					m.tabDrop = tabHitPlus // out to a tab of its own
				default:
					m.tabDrop = tabDropNone // the gap, or the close button
				}
				return m, nil
			}
			m.tabDrop = tabDropNone
			// What it would swap with, while the button is held: the drop
			// is marked as well as the split being carried.
			rects, _ := m.leafRects()
			if id := m.leafAt(rects, msg.X, msg.Y); id != m.leafDrag {
				m.leafDrop = id
			} else {
				m.leafDrop = 0
			}
			return m, nil
		}
		return m, nil
	}

	// Resizing splits by dragging the boundary between two leaves; the drag
	// ends wherever the button is released, over the sidebar or status bar too.
	if m.barDrag != nil {
		bar := m.barDrag
		switch msg.Action {
		case tea.MouseActionMotion:
			if bar.node.dir == splitRight {
				bar.node.ratio = float64(msg.X-bar.area.x) / float64(max(bar.area.w, 1))
			} else {
				bar.node.ratio = float64(msg.Y-bar.area.y) / float64(max(bar.area.h, 1))
			}
			bar.node.ratio = min(max(bar.node.ratio, 0.05), 0.95)
			return m, nil
		case tea.MouseActionRelease:
			m.barDrag = nil
			return m, tea.Batch(m.syncView(), m.saveState())
		}
		return m, nil
	}

	// A selection being dragged goes wherever the pointer does: over the
	// tab bar, the status bar or the tree, and past the pane's edge the
	// history scrolls to take more.
	if cmd, ok := m.dragSelection(msg); ok {
		return m, cmd
	}

	// Status bar: every hint, the waiting counter and Settings are buttons.
	if msg.Y == m.height-1 {
		if press && left {
			return m, m.clickStatus(msg.X)
		}
		return m, nil
	}

	// The sidebar's right border and the main area's left border sit side
	// by side, and people grab whichever they see: either starts a resize.
	// The tab bar's row is left to the tabs.
	if !m.zoom && press && left && (msg.X == m.sidebarW-1 || msg.X == m.sidebarW) && msg.Y > m.mainRect().y {
		var cmd tea.Cmd
		if msg.X == m.sidebarW {
			// It is also that split's border, and a press on a pane's
			// border focuses the pane: still true when it becomes a drag.
			rects, _ := m.leafRects()
			if id := m.leafAt(rects, msg.X, msg.Y); id != 0 && id != m.tab().focus {
				cmd = m.focusLeaf(id)
			}
			if m.tab().focused().view.Kind == kindPane {
				m.focus = focusMain
			}
		}
		m.dragging = true
		return m, cmd
	}
	if !m.zoom && msg.X < m.sidebarW {
		return m.sidebarMouse(msg, press, left, wheel)
	}

	mr := m.mainRect()
	if !m.zoom && msg.Y == mr.y {
		if press && left {
			_, hits := m.tabBar(mr.w)
			for _, h := range hits {
				if x := msg.X - mr.x; x >= h.x0 && x < h.x1 {
					switch h.tab {
					case -1:
						m.overlay = newTabMenu(m, mr.x+h.x0, mr.y+1)
						return m, nil
					case -2:
						return m, m.closeTabAsk(m.activeTab)
					}
					cmd := m.switchTab(h.tab)
					m.tabDrag = len(m.visibleTabs()) > 1 && !m.previewing
					// A tab of an agent or terminal is there to type into.
					if v := m.tab().focused().view; v.Kind == kindPane {
						if p := m.pane(v.Machine, v.PaneID); p != nil && p.State == proto.PaneRunning {
							m.focus = focusMain
						}
					}
					return m, cmd
				}
			}
		}
		return m, nil
	}

	rects, bars := m.leafRects()
	if press && left {
		for i := range bars {
			bar := bars[i]
			onBar := false
			if bar.node.dir == splitRight {
				onBar = (msg.X == bar.pos-1 || msg.X == bar.pos) && msg.Y >= bar.area.y && msg.Y < bar.area.y+bar.area.h
			} else {
				onBar = (msg.Y == bar.pos-1 || msg.Y == bar.pos) && msg.X >= bar.area.x && msg.X < bar.area.x+bar.area.w
			}
			// Only the border lines themselves, not the leaves' contents.
			if onBar && !m.inner(rects[m.leafAt(rects, msg.X, msg.Y)]).contains(msg.X, msg.Y) {
				m.barDrag = &bar
				return m, nil
			}
		}
	}

	if press && left && !m.zoom {
		if id := m.leafAt(rects, msg.X, msg.Y); id != 0 && msg.X == rects[id].x+rects[id].w-1 {
			if l := m.tab().leaf(id); l != nil && l.view.Kind == kindPane {
				if f := m.frames[paneKey(l.view.Machine, l.view.PaneID)]; f != nil && f.History > 0 {
					cmd := m.focusLeaf(id)
					m.scrollDrag = id
					m.scrollTop = rects[id].y + 1
					m.scrollH = max(rects[id].h-2, 1)
					m.scrollTo(msg.Y)
					return m, cmd
				}
			}
		}
	}

	// A split's title is a grip when there is somewhere to carry it: another
	// split to swap with, or another tab to move it into — the only split of
	// a tab is still worth dragging if the bar has somewhere to put it.
	if press && left && !m.zoom && (len(m.tab().root.leaves()) > 1 || len(m.visibleTabs()) > 1) {
		if id := m.leafAt(rects, msg.X, msg.Y); id != 0 && msg.Y == rects[id].y &&
			!m.inner(rects[id]).contains(msg.X, msg.Y) {
			m.leafDrag, m.tabDrop = id, tabDropNone
			return m, m.focusLeaf(id)
		}
	}

	t := m.tab()
	var focusCmd tea.Cmd
	if id := m.leafAt(rects, msg.X, msg.Y); id != 0 && id != t.focus && (press || wheel) && !(m.sel != nil && m.sel.dragging) {
		focusCmd = m.focusLeaf(id)
	}
	f := t.focused()
	in := m.inner(rects[f.id])
	x, y := msg.X-in.x, msg.Y-in.y
	if x < 0 || y < 0 || x >= in.w || y >= in.h {
		if press && left && f.view.Kind == kindPane {
			m.focus = focusMain
		}
		return m, focusCmd
	}
	if f.view.Kind != kindPane && press && left {
		// A press on a page may start a selection; what it clicks is
		// decided on release, once it is clear nothing was dragged.
		m.focus = focusMain
		m.click = &pendingClick{msg: msg, x: x, y: y}
		sel := &selection{leaf: f.id, ax: x, ay: y, bx: x, by: y, dragging: true}
		sel.colFrom, sel.colTo = pageColumns(f, x, in.w)
		m.sel = sel
		return m, focusCmd
	}
	cmd := m.viewMouse(f, msg, x, y) // before m is returned: it changes m
	return m, tea.Batch(focusCmd, cmd)
}

// viewMouse passes a mouse event to what leaf f shows, at x, y inside it.
func (m *Model) viewMouse(f *leaf, msg tea.MouseMsg, x, y int) tea.Cmd {
	press := msg.Action == tea.MouseActionPress
	left := msg.Button == tea.MouseButtonLeft
	wheel := msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown
	switch f.view.Kind {
	case kindPane:
		return m.paneMouse(f.view.Machine, f.view.PaneID, msg, x, y, press, wheel)
	case kindReviewQueue:
		if press && left {
			m.focus = focusMain
		}
		if f.queue != nil {
			return f.queue.mouse(m, msg, x, y)
		}
	case kindSessions:
		if press && left {
			m.focus = focusMain
		}
		if f.sessions != nil {
			return f.sessions.mouse(m, msg, x, y)
		}
	case kindFiles:
		if press && left {
			m.focus = focusMain
		}
		if f.files != nil {
			return f.files.mouse(m, msg, x, y)
		}
	case kindBranch:
		if press && left {
			m.focus = focusMain
		}
		if f.changes != nil {
			return f.changes.mouse(m, msg, x, y)
		}
	case kindBranches:
		if press && left {
			return m.clickBranch(f.view, y)
		}
	case kindAgents, kindTerminals, kindSSH:
		if press && left {
			return m.clickSectionPane(f.view, y)
		}
	}
	return nil
}

// branchesPageHeader is how many lines come before the first branch on the
// Branches page: its title, the path and a blank line.
const branchesPageHeader = 3

// clickBranch opens the branch on line y of the Branches page.
func (m *Model) clickBranch(v viewRef, y int) tea.Cmd {
	proj := m.project(v.Machine, v.ProjectID)
	if proj == nil {
		return nil
	}
	var panes []proto.PaneInfo
	if mach := m.machine(v.Machine); mach != nil {
		panes = mach.panes
	}
	branches, _ := listedBranches(*proj, panes, true, time.Now())
	i := y - branchesPageHeader
	if i < 0 || i >= len(branches) {
		return nil // the title, the hint, or past the last branch
	}
	return m.openBranch(v.Machine, v.ProjectID, branches[i].Name)
}

// clickSectionPane opens the pane on line y of an Agents, Terminals or SSH page.
// An agent's summary takes a line of its own, so the lines are counted the
// way the page lays them out.
func (m *Model) clickSectionPane(v viewRef, y int) tea.Cmd {
	line := branchesPageHeader // title, machine, blank — the same shape
	for _, p := range m.sectionPanes(v.Machine, v.ProjectID, v.Kind) {
		if y == line {
			return m.openPaneRow(v.Machine, p.ID)
		}
		line++
		if m.summaryText(v.Machine, p.ID) != "" {
			if y == line {
				return m.openPaneRow(v.Machine, p.ID) // its summary line
			}
			line++
		}
	}
	return nil
}

// openPaneRow selects a pane in the tree and shows it.
func (m *Model) openPaneRow(mid, paneID string) tea.Cmd {
	i := indexOfRow(m.rows, paneNodeID(mid, paneID))
	if i < 0 {
		return nil
	}
	m.cursor = m.rows[i].id
	return m.show(m.rows[i])
}

// openBranch selects a branch in the tree and shows its changes. A branch
// the tree keeps behind "… more" is listed first, so the click still lands
// somewhere.
func (m *Model) openBranch(mid, pid, branch string) tea.Cmd {
	id := branchNodeID(mid, pid, branch)
	var cmds []tea.Cmd
	if indexOfRow(m.rows, id) < 0 {
		m.showAll[scoped(mid, pid)] = true
		m.expanded[projectNodeID(mid, pid)] = true
		m.expanded[sectionID(mid, pid, "branches")] = true
		cmds = append(cmds, m.rebuild(), m.saveState())
	}
	i := indexOfRow(m.rows, id)
	if i < 0 {
		return tea.Batch(cmds...)
	}
	m.cursor = id
	return tea.Batch(append(cmds, m.show(m.rows[i]))...)
}

// leafAt returns the leaf whose area holds x, y, or 0.
func (m Model) leafAt(rects map[int]rect, x, y int) int {
	for id, r := range rects {
		if r.contains(x, y) {
			return id
		}
	}
	return 0
}

// paneMouse handles the mouse over a pane. Programs that asked for mouse
// input get it; otherwise the wheel scrolls history (or sends arrow keys to
// full-screen programs) and dragging selects text.
func (m *Model) paneMouse(machine, paneID string, msg tea.MouseMsg, x, y int, press, wheel bool) tea.Cmd {
	// The pane under the pointer, which is not always the focused one: a
	// click or a wheel over another split asks for that split's focus, and
	// the asking is a command that has not run yet. Deciding by the
	// focused pane's frame sent SGR mouse reports to a pane that had never
	// asked for the mouse, which printed them as text into an agent's
	// prompt (`<65;106;43M`), and would have sent them to another
	// machine's client as well.
	c := m.clientOf(machine)
	if c == nil {
		return nil
	}
	if press && !wheel {
		m.focus = focusMain
	}
	f := m.frames[paneKey(machine, paneID)]
	// Scrolling conch's own history is the focused pane's business: its
	// offset and selection are what move. A wheel over another split has
	// just asked for that split's focus, so the next one scrolls it.
	ours := machine == m.viewMachine && paneID == m.viewing
	// A link an agent printed, before anything else looks at the click: in
	// a pane whose program takes the mouse it needs alt or ctrl, since that
	// program is owed its clicks; anywhere else a plain click opens it,
	// because nothing else was using it.
	if cmd, took := m.clickedLink(f, msg, x, y); took {
		return cmd
	}
	switch {
	case f != nil && f.Mouse && !wheel && (m.selectsOverApp(paneID) || msg.Alt || msg.Ctrl):
		return m.selectOrClick(c, paneID, msg, x, y)
	case ours && f != nil && f.Mouse && wheel && m.sel != nil && m.sel.paneID == paneID && f.History > 0:
		// Text selected in a program that takes the mouse, in a pane conch
		// has history for: the wheel scrolls that history while the
		// selection lasts, so it can be taken past the top of the screen.
		delta := -3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = 3
		}
		m.scrollPane(delta)
	case f != nil && f.Mouse && wheel && m.sel != nil && m.sel.paneID == paneID:
		// An agent on the alternate screen keeps its own scrollback and
		// conch has none to offer, so the wheel goes to the program as
		// usual. The text underneath then moves, and a selection held over
		// it would be a lie, so it goes.
		m.sel = nil
		forwardMouse(c, paneID, msg, x, y)
	case f != nil && f.Mouse:
		forwardMouse(c, paneID, msg, x, y)
	case wheel && f != nil && f.AltScreen:
		key := "down"
		if msg.Button == tea.MouseButtonWheelUp {
			key = "up"
		}
		c.Notify(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: paneID, Keys: []string{key, key, key}})
	case wheel && !ours:
		// Not the pane on screen yet: this wheel asked for its focus, and
		// scrolling would have moved the one that is. The next turns it.
	case wheel:
		delta := -3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = 3
		}
		m.scrollPane(delta)
	default:
		return m.selectMouse(msg, x, y)
	}
	return nil
}

// selectsOverApp reports whether dragging selects text even though the
// program asked for mouse events: agents use the mouse for clicks and the
// wheel, and their output is what people want to copy.
func (m Model) selectsOverApp(paneID string) bool {
	p := m.pane(m.viewMachine, paneID)
	return p != nil && p.Agent != nil
}

// pendingClick is a left press held back from a program until it is clear
// whether it starts a drag (a selection) or is a click (forwarded).
type pendingClick struct {
	msg  tea.MouseMsg
	x, y int
}

// selectOrClick selects text by dragging in a pane whose program takes the
// mouse, and passes plain clicks through to it.
func (m *Model) selectOrClick(c interface{ Notify(string, any) }, paneID string, msg tea.MouseMsg, x, y int) tea.Cmd {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		m.click = &pendingClick{msg: msg, x: x, y: y}
		return m.selectMouse(msg, x, y)
	case msg.Action == tea.MouseActionMotion && m.sel != nil && m.sel.dragging:
		return m.selectMouse(msg, x, y)
	case msg.Action == tea.MouseActionRelease && m.click != nil:
		dragged := m.sel != nil && m.sel.hasContent
		cmd := m.selectMouse(msg, x, y)
		if !dragged {
			forwardMouse(c, paneID, m.click.msg, m.click.x, m.click.y)
			forwardMouse(c, paneID, msg, x, y)
		}
		m.click = nil
		return cmd
	}
	forwardMouse(c, paneID, msg, x, y)
	return nil
}

// selectMouse drives text selection: press anchors, drag extends, release
// copies. A double click selects and copies the word under the pointer.
func (m *Model) selectMouse(msg tea.MouseMsg, x, y int) tea.Cmd {
	cols, _ := m.paneArea()
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		now := time.Now()
		key := fmt.Sprintf("sel:%d:%d", x, y)
		if m.lastClickID == key && now.Sub(m.lastClickAt) < doubleClickWindow {
			m.lastClickN++
		} else {
			m.lastClickN = 1
		}
		m.lastClickID, m.lastClickAt = key, now
		// Twice takes the word, three times the line — what every other
		// terminal does, and the line without the blanks it is padded to.
		if m.lastClickN >= 2 && m.frame != nil && y < len(m.frame.Lines) {
			from, to := wordAt(m.frame.Lines[y], x)
			ay, by := y, y
			if m.lastClickN >= 3 {
				// The line, not the row: a line too long for the pane is
				// several rows, and taking one of them is half a command.
				ay, by = wrappedLine(m.frame.Lines, y, cols)
				from, _ = lineAt(m.frame.Lines[ay])
				_, to = lineAt(m.frame.Lines[by])
			}
			if to > from || by > ay {
				m.sel = &selection{paneID: m.viewing, ax: from, ay: ay, bx: to, by: by, hasContent: true}
				return copyText(m.sel.text(m.frame.Lines, cols))
			}
		}
		m.sel = &selection{paneID: m.viewing, ax: x, ay: y, bx: x, by: y, dragging: true}
		m.rememberSel()
	case msg.Action == tea.MouseActionMotion && m.sel != nil && m.sel.dragging:
		m.sel.bx, m.sel.by = x, y
		m.sel.hasContent = m.sel.hasContent || x != m.sel.ax || y != m.sel.ay
	case msg.Action == tea.MouseActionRelease && m.sel != nil && m.sel.dragging:
		m.sel.dragging = false
		if !m.sel.hasContent || m.frame == nil {
			m.sel = nil
			return nil
		}
		return copyText(m.selText())
	}
	return nil
}

// rememberSel keeps the text of the pane rows on screen with the
// selection, so it still copies them once they have scrolled away. A frame
// not yet caught up with a scroll is passed over: its rows are not where
// the selection thinks they are.
func (m *Model) rememberSel() {
	if m.sel != nil && m.sel.leaf == 0 && m.frame != nil && m.frame.Offset == m.offset {
		m.sel.remember(m.frame.Lines)
	}
}

// selText is the text of the selection in the viewed pane, with the rows
// that have scrolled out of sight.
func (m *Model) selText() string {
	if m.sel == nil {
		return ""
	}
	cols, _ := m.paneArea()
	m.rememberSel()
	var lines []string
	if m.frame != nil && m.frame.Offset == m.offset {
		lines = m.frame.Lines
	}
	return m.sel.text(lines, cols)
}

// dragSelection carries on a selection while its button is held, wherever
// the pointer goes. It reports whether it took the event: a held click
// inside its own pane or page is left to the usual handling, which settles
// it on release.
func (m *Model) dragSelection(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.sel == nil || !m.sel.dragging {
		return nil, false
	}
	rects, _ := m.leafRects()
	id := m.tab().focus
	if m.sel.leaf != 0 {
		id = m.sel.leaf
	}
	r, ok := rects[id]
	if !ok { // its leaf went while the button was down
		m.sel, m.click, m.selEdge = nil, nil, 0
		return nil, false
	}
	in := m.inner(r)
	x, y := msg.X-in.x, msg.Y-in.y
	outside := x < 0 || y < 0 || x >= in.w || y >= in.h
	cx, cy := clamp(x, 0, in.w-1), clamp(y, 0, in.h-1)

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if m.sel.leaf != 0 {
			// A page scrolls under the selection, which would then be over
			// other text: it goes, and the page takes the wheel.
			m.sel, m.click = nil, nil
			return nil, false
		}
		if m.frame != nil && m.frame.History > 0 {
			delta := -3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = 3
			}
			m.scrollPane(delta)
			return nil, true
		}
		return nil, m.click == nil || outside
	}

	if m.sel.leaf != 0 {
		return m.dragPageSelection(msg, id, in, cx, cy), true
	}
	if m.click != nil && !outside {
		m.selEdge = 0
		return nil, false // selectOrClick settles it
	}
	var tick tea.Cmd
	switch msg.Action {
	case tea.MouseActionRelease:
		m.click, m.selEdge = nil, 0
	case tea.MouseActionMotion:
		m.selEdge = 0
		switch {
		case y < 0:
			m.selEdge = min(-y, 3) // above the top: back into history
		case y >= in.h:
			m.selEdge = -min(y-in.h+1, 3)
		}
		if m.selEdge != 0 && !m.selTicking {
			m.selTicking = true
			tick = m.selAutoScroll()
		}
	}
	return tea.Batch(m.selectMouse(msg, cx, cy), tick), true
}

// selScrollMsg keeps a selection scrolling while the pointer is held past
// its pane's top or bottom edge, as terminals do.
type selScrollMsg struct{}

const selScrollEvery = 60 * time.Millisecond

// selAutoScroll scrolls the pane under a selection held past its edge,
// and schedules the next step until the drag ends, the pointer comes back
// in, or the history runs out.
func (m *Model) selAutoScroll() tea.Cmd {
	if m.sel == nil || !m.sel.dragging || m.sel.leaf != 0 || m.selEdge == 0 {
		m.selTicking = false
		return nil
	}
	before := m.offset
	m.scrollPane(m.selEdge)
	if m.offset == before {
		m.selTicking = false
		return nil
	}
	return tea.Tick(selScrollEvery, func(time.Time) tea.Msg { return selScrollMsg{} })
}

// dragPageSelection drives a selection over the page leaf id shows: drag
// extends it, release copies it — or, when nothing was dragged, passes the
// held click to the page.
func (m *Model) dragPageSelection(msg tea.MouseMsg, id int, in rect, x, y int) tea.Cmd {
	switch msg.Action {
	case tea.MouseActionMotion:
		m.sel.bx, m.sel.by = x, y
		m.sel.hasContent = m.sel.hasContent || x != m.sel.ax || y != m.sel.ay
	case tea.MouseActionRelease:
		click := m.click
		m.sel.dragging, m.click = false, nil
		if m.sel.hasContent {
			return copyText(m.sel.text(m.pageLines(id, in), in.w))
		}
		m.sel = nil
		l := m.tab().leaf(id)
		if click == nil || l == nil {
			return nil
		}
		return tea.Batch(m.viewMouse(l, click.msg, click.x, click.y), m.viewMouse(l, msg, x, y))
	}
	return nil
}

// pageLines is what leaf id shows in its inner area, as drawn.
func (m Model) pageLines(id int, in rect) []string {
	t := m.tab()
	l := t.leaf(id)
	if l == nil {
		return nil
	}
	return exactly(m.leafBody(l, in.w, in.h, id == t.focus), in.h)
}

func (m Model) sidebarMouse(msg tea.MouseMsg, press, left, wheel bool) (tea.Model, tea.Cmd) {
	if wheel {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		m.scroll = clamp(m.scroll+delta, 0, max(len(m.rows)-m.sidebarRowsVisible(), 0))
		return m, nil
	}
	if !press {
		return m, nil
	}
	if msg.Y == 0 { // the top border: conch, the workspaces and a +
		if left {
			return m, m.clickSpace(msg.X)
		}
		return m, nil
	}
	// Row 0 of the content is the header; the border takes one line.
	i := m.scroll + msg.Y - 2
	if msg.Y < 2 || i < 0 || i >= len(m.rows) {
		if msg.Y == 1 && left {
			m.filtering = true
		}
		return m, nil
	}
	r := m.rows[i]
	// Grouped by tab, a pane row can be carried to a tab's section. The
	// press still selects and shows the row, as it always did: a drag is
	// only a drag once the pointer has moved onto a section.
	if left && r.kind == kindPane && (treeGroups(m.cfg.UI.TreeGroups) == "tabs" || len(m.folders) > 0) {
		m.rowDrag, m.rowDrop = r.id, ""
	}
	wasSelected := r.id == m.cursor
	m.cursor = r.id
	m.focus = focusSidebar
	cmd := m.syncView()
	if left && r.kind == kindSavedSSH {
		// A saved host has nothing to show until it is connected. With an
		// SSH folder to drag it to, the press may start a drag, and the
		// click connects on release instead.
		if len(m.folders[sshFolderKey()]) > 0 {
			m.rowDrag, m.rowDrop = r.id, ""
			return m, cmd
		}
		return m, tea.Batch(cmd, m.connectSSH(savedSSHTarget(r.id)))
	}
	if left && !r.expandable() {
		// A click puts the row in the focused split. Keep what syncView
		// asked for: it loads a branch's changes, and show() won't ask
		// again — the view it would load into already exists.
		cmd = tea.Batch(cmd, m.show(r))
	}

	switch msg.Button {
	case tea.MouseButtonRight:
		m.overlay = newRowMenu(m, r, msg.X, msg.Y)
		return m, cmd
	case tea.MouseButtonLeft:
		now := time.Now()
		double := wasSelected && m.lastClickID == r.id && now.Sub(m.lastClickAt) < doubleClickWindow
		m.lastClickID, m.lastClickAt = r.id, now
		// The expander arrow sits after the indent (border + 2 per level).
		onExpander := msg.X >= 1+r.depth*2 && msg.X <= 2+r.depth*2
		switch {
		case r.kind == kindMore:
			return m, tea.Batch(cmd, m.toggle(r, nil))
		case r.expandable() && (onExpander || wasSelected):
			return m, tea.Batch(cmd, m.toggle(r, nil))
		case double:
			return m, tea.Batch(cmd, m.activate(r))
		}
	}
	return m, cmd
}

// forwardMouse passes a mouse event to a pane at pane-relative x, y. The
// server drops it unless the program asked for mouse input.
func forwardMouse(c interface {
	Notify(string, any)
}, paneID string, msg tea.MouseMsg, x, y int) {
	p := proto.PaneSendMouseParams{ID: paneID, X: x, Y: y, Shift: msg.Shift, Alt: msg.Alt, Ctrl: msg.Ctrl}
	switch msg.Button {
	case tea.MouseButtonLeft:
		p.Button = "left"
	case tea.MouseButtonMiddle:
		p.Button = "middle"
	case tea.MouseButtonRight:
		p.Button = "right"
	case tea.MouseButtonWheelUp:
		p.Button, p.Action = "wheel_up", proto.MouseWheel
	case tea.MouseButtonWheelDown:
		p.Button, p.Action = "wheel_down", proto.MouseWheel
	case tea.MouseButtonNone:
		p.Button = "none"
	default:
		return
	}
	if p.Action == "" {
		switch msg.Action {
		case tea.MouseActionPress:
			p.Action = proto.MousePress
		case tea.MouseActionRelease:
			p.Action = proto.MouseRelease
		case tea.MouseActionMotion:
			p.Action = proto.MouseMotion
		}
	}
	c.Notify(proto.MethodPaneSendMouse, p)
}

// pageColumns is the column range a selection started at x belongs to, for
// a page that draws in columns. Everything else selects the whole width.
func pageColumns(l *leaf, x, w int) (from, to int) {
	if l != nil && l.files != nil {
		return l.files.columns(x, w)
	}
	return 0, 0
}

// rowAtY is the tree row at a point in the sidebar, if the point is in the
// sidebar and on a row at all. Row 0 of the content is the header and the
// border takes a line, as sidebarMouse counts them.
func (m Model) rowAtY(x, y int) (row, bool) {
	if m.zoom || x < 0 || x >= m.sidebarW || y < 2 {
		return row{}, false
	}
	i := m.scroll + y - 2
	if i < 0 || i >= len(m.rows) {
		return row{}, false
	}
	return m.rows[i], true
}

// dropRowOnTab moves the pane a tree row stands for into the tab another
// row stands for. Only a pane goes: a branch or a section is not a thing a
// tab holds one of.
func (m *Model) dropRowOnTab(fromID, toID string) tea.Cmd {
	var from, to row
	for _, r := range m.rows {
		switch r.id {
		case fromID:
			from = r
		case toID:
			to = r
		}
	}
	// A saved host, or a session to one, goes in an SSH folder as the
	// host, so its sessions follow it.
	if cmd, ok := m.dropOnSSHFolder(from, to); ok {
		return cmd
	}
	if from.kind != kindPane || from.paneID == "" {
		return nil
	}
	p := m.pane(from.machine, from.paneID)
	switch {
	case to.kind == kindTab:
		return m.movePaneToTab(from.machine, from.paneID, to.tabIndex)
	case to.kind == kindFolder && p != nil:
		// Into a folder of its own section: a pane moved to a folder of
		// another section would be listed in neither.
		if to.machine != from.machine || to.section != m.paneSection(from.machine, from.paneID) {
			return nil
		}
		if !m.putInFolder(to.machine, to.projectID, to.section, to.label, *p) {
			return nil
		}
		m.expanded[to.id] = true
		return tea.Batch(m.rebuild(), m.saveState())
	case m.sectionOf(to) && p != nil:
		// Out of whatever folder it is in, back to the section's own list.
		if to.machine != from.machine || to.kind != m.paneSection(from.machine, from.paneID) {
			return nil
		}
		if !m.takeOutOfFolders(to.machine, to.projectID, to.kind, *p) {
			return nil
		}
		return tea.Batch(m.rebuild(), m.saveState())
	}
	return nil
}

// sectionOf reports whether a row is a section panes are listed under, and
// so somewhere a pane can be dragged out of a folder onto.
func (m Model) sectionOf(r row) bool {
	switch r.kind {
	case kindAgents, kindTerminals, kindSSH:
		return true
	}
	return false
}
