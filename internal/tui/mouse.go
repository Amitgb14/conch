package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.overlay != nil {
		return m, m.overlay.mouse(&m, msg, m.overlay.render(m))
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

	// Status bar: every hint, the waiting counter and Settings are buttons.
	if msg.Y == m.height-1 {
		if press && left {
			return m, m.clickStatus(msg.X)
		}
		return m, nil
	}

	if !m.zoom && msg.X < m.sidebarW {
		if press && left && msg.X == m.sidebarW-1 {
			m.dragging = true
			return m, nil
		}
		return m.sidebarMouse(msg, press, left, wheel)
	}

	// Resizing splits by dragging the boundary between two leaves.
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

	mr := m.mainRect()
	if !m.zoom && msg.Y == mr.y {
		if press && left {
			_, hits := m.tabBar(mr.w)
			for _, h := range hits {
				if x := msg.X - mr.x; x >= h.x0 && x < h.x1 {
					switch h.tab {
					case -1:
						return m, m.newTab(viewRef{})
					case -2:
						return m, m.closeTab(m.activeTab)
					}
					return m, m.gotoTab(h.tab)
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

	t := m.tab()
	var focusCmd tea.Cmd
	if id := m.leafAt(rects, msg.X, msg.Y); id != 0 && id != t.focus && (press || wheel) && !(m.sel != nil && m.sel.dragging) {
		focusCmd = m.focusLeaf(id)
	}
	f := t.focused()
	in := m.inner(rects[f.id])
	x, y := msg.X-in.x, msg.Y-in.y
	// A selection drag keeps going when the pointer leaves the pane.
	if f.view.Kind == kindPane && m.sel != nil && m.sel.dragging {
		return m, m.selectMouse(msg, clamp(x, 0, in.w-1), clamp(y, 0, in.h-1))
	}
	if x < 0 || y < 0 || x >= in.w || y >= in.h {
		if press && left && f.view.Kind == kindPane {
			m.focus = focusMain
		}
		return m, focusCmd
	}
	switch f.view.Kind {
	case kindPane:
		return m, tea.Batch(focusCmd, m.paneMouse(f.view.PaneID, msg, x, y, press, wheel))
	case kindSessions:
		if press && left {
			m.focus = focusMain
		}
		if f.sessions != nil {
			return m, tea.Batch(focusCmd, f.sessions.mouse(&m, msg, x, y))
		}
	case kindBranch:
		if press && left {
			m.focus = focusMain
		}
		if f.changes != nil {
			return m, tea.Batch(focusCmd, f.changes.mouse(&m, msg, x, y))
		}
	}
	return m, focusCmd
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
func (m *Model) paneMouse(paneID string, msg tea.MouseMsg, x, y int, press, wheel bool) tea.Cmd {
	c := m.viewClient()
	if c == nil {
		return nil
	}
	if press && !wheel {
		m.focus = focusMain
	}
	f := m.frame
	switch {
	case f != nil && f.Mouse:
		forwardMouse(c, paneID, msg, x, y)
	case wheel && f != nil && f.AltScreen:
		key := "down"
		if msg.Button == tea.MouseButtonWheelUp {
			key = "up"
		}
		c.Notify(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: paneID, Keys: []string{key, key, key}})
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

// selectMouse drives text selection: press anchors, drag extends, release
// copies. A double click selects and copies the word under the pointer.
func (m *Model) selectMouse(msg tea.MouseMsg, x, y int) tea.Cmd {
	cols, _ := m.paneArea()
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		now := time.Now()
		key := fmt.Sprintf("sel:%d:%d", x, y)
		double := m.lastClickID == key && now.Sub(m.lastClickAt) < doubleClickWindow
		m.lastClickID, m.lastClickAt = key, now
		if double && m.frame != nil && y < len(m.frame.Lines) {
			from, to := wordAt(m.frame.Lines[y], x)
			if to > from {
				m.sel = &selection{paneID: m.viewing, ax: from, ay: y, bx: to, by: y, hasContent: true}
				return copyText(m.sel.text(m.frame.Lines, cols))
			}
		}
		m.sel = &selection{paneID: m.viewing, ax: x, ay: y, bx: x, by: y, dragging: true}
	case msg.Action == tea.MouseActionMotion && m.sel != nil && m.sel.dragging:
		m.sel.bx, m.sel.by = x, y
		m.sel.hasContent = m.sel.hasContent || x != m.sel.ax || y != m.sel.ay
	case msg.Action == tea.MouseActionRelease && m.sel != nil && m.sel.dragging:
		m.sel.dragging = false
		if !m.sel.hasContent || m.frame == nil {
			m.sel = nil
			return nil
		}
		return copyText(m.sel.text(m.frame.Lines, cols))
	}
	return nil
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
	// Row 0 of the content is the header; the border takes one line.
	i := m.scroll + msg.Y - 2
	if msg.Y < 2 || i < 0 || i >= len(m.rows) {
		if msg.Y == 1 && left {
			m.filtering = true
		}
		return m, nil
	}
	r := m.rows[i]
	wasSelected := r.id == m.cursor
	m.cursor = r.id
	m.focus = focusSidebar
	cmd := m.syncView()
	if left && !r.expandable() {
		cmd = m.show(r) // a click puts it in the focused split
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
