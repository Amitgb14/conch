package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Hover: the row, tab or split under the pointer says so before it is
// clicked. It costs a message per cell the pointer crosses — the terminal
// is asked to report every move rather than only drags — so it is behind
// [ui] hover, and everything it touches works the same without it.
//
// The one thing that must not change when it is on: a program in a pane
// asked for the mouse it had, not this one. Motion with no button held is
// conch's own business and is never forwarded.

// hoverAt remembers what the pointer is over, and says whether anything
// changed and so needs drawing again.
func (m *Model) hoverAt(msg tea.MouseMsg) bool {
	if !m.cfg.UI.Hover || msg.Action != tea.MouseActionMotion || msg.Button != tea.MouseButtonNone {
		return false
	}
	row, tab := "", -1
	switch {
	case msg.X < m.sidebarW && !m.zoom:
		// Row 0 of the sidebar's content is the header, and the border
		// takes a line — the same arithmetic its clicks use.
		if i := m.scroll + msg.Y - 2; msg.Y >= 2 && i >= 0 && i < len(m.rows) {
			row = m.rows[i].id
		}
	case !m.zoom && msg.Y == m.mainRect().y:
		mr := m.mainRect()
		_, hits := m.tabBar(mr.w)
		for _, h := range hits {
			if x := msg.X - mr.x; x >= h.x0 && x < h.x1 && h.tab >= 0 {
				tab = h.tab
			}
		}
	}
	if row == m.hoverRow && tab == m.hoverTab {
		return false
	}
	m.hoverRow, m.hoverTab = row, tab
	return true
}

// clearHover forgets what was under the pointer — when the mouse leaves,
// when a dialog opens over everything, or when hover is turned off.
func (m *Model) clearHover() {
	m.hoverRow, m.hoverTab = "", -1
}

// hovering reports whether a tree row is the one under the pointer.
func (m Model) hovering(id string) bool {
	return m.cfg.UI.Hover && id != "" && id == m.hoverRow
}
