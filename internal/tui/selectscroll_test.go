package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// A selection copies rows it remembered once they are off the screen, and
// the rows move with the history as it scrolls.
func TestSelectionRememberedRows(t *testing.T) {
	s := selection{ax: 2, ay: -1, bx: 1, by: 1}
	if got := s.text([]string{"abcd", "efgh"}, 6); got != "abcd\nef" {
		t.Fatalf("nothing remembered: %q", got)
	}
	s.remember([]string{"\x1b[1mzzzz\x1b[0m"})
	s.shiftRows(-1) // it scrolled up a line: row 0 is now row -1
	s.shiftRows(0)
	if got := s.text([]string{"abcd", "efgh"}, 6); got != "zz\nabcd\nef" {
		t.Fatalf("with a remembered row: %q", got)
	}
	// Rows never seen are left out rather than made up.
	s = selection{ax: 0, ay: 5, bx: 1, by: 0}
	if got := s.text([]string{"ab"}, 2); got != "b" {
		t.Fatalf("unseen rows: %q", got)
	}
	var empty selection
	empty.shiftRows(3)
	if empty.rows != nil {
		t.Fatal("shifting nothing made rows")
	}
}

// selScrollFixture shows terminal p3 with 60 lines of history on a pane
// whose frames come from serve, as the server would send them.
func selScrollFixture(t *testing.T, frame proto.Frame) (m *Model, in rect, serve func()) {
	t.Helper()
	m, _ = a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, frame.ID))
	in = m.focusedRect()
	all := make([]string, 60+in.h)
	for i := range all {
		all[i] = fmt.Sprintf("line %02d", i)
	}
	serve = func() {
		o := m.offset
		f := frame
		f.Lines, f.Offset, f.History = all[len(all)-in.h-o:len(all)-o], o, 60
		m.subscribed[paneKey(localMachine, f.ID)] = true
		m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneFrame, f))
	}
	serve()
	m.syncView()
	return m, in, serve
}

// Holding a drag above the pane scrolls its history, and the copy takes
// every line passed over, not just the ones left on the screen.
func TestDragPastTheTopScrollsAndCopiesAll(t *testing.T) {
	m, in, serve := selScrollFixture(t, proto.Frame{ID: "p3"})
	lastClipboard = ""
	a1Mouse(t, m, in.x, in.y+2, a1Left, a1Press)
	anchor := m.sel.ay
	first := ansi.Strip(m.frame.Lines[anchor])

	// Up over the tab bar: the pane scrolls back and keeps scrolling.
	cmd := a1Mouse(t, m, in.x+3, in.y-2, tea.MouseButtonNone, a1Motion)
	if m.selEdge != 2 || !m.selTicking || m.offset != 2 || cmd == nil {
		t.Fatalf("edge %d ticking %v offset %d", m.selEdge, m.selTicking, m.offset)
	}
	serve()
	for range 5 {
		next, cmd := m.Update(selScrollMsg{})
		*m = next.(Model)
		if cmd == nil {
			t.Fatal("the scroll stopped while the pointer was still held above")
		}
		serve()
	}
	if m.offset != 12 || m.sel.ay != anchor+12 || m.sel.by != 0 {
		t.Fatalf("offset %d sel %+v", m.offset, *m.sel)
	}
	top := ansi.Strip(m.frame.Lines[0])

	// Released over the status bar: still the selection's, and copied.
	a2Run(a1Mouse(t, m, in.x+3, m.height-1, a1Left, a1Release))
	lines := strings.Split(lastClipboard, "\n")
	if len(lines) != 15 || lines[0] != top[3:] || lines[len(lines)-1] != first[:1] {
		t.Fatalf("copied %d lines: %q", len(lines), lastClipboard)
	}
	if m.selEdge != 0 || m.overlay != nil {
		t.Fatalf("after release: edge %d overlay %T", m.selEdge, m.overlay)
	}
	// With the drag over, the next tick lets it rest.
	next, cmd := m.Update(selScrollMsg{})
	*m = next.(Model)
	if cmd != nil || m.selTicking {
		t.Fatal("still scrolling after the drag ended")
	}
}

// Past the bottom the drag heads back towards the live screen, and stops
// there: nothing lies beyond it.
func TestDragPastTheBottomStopsAtLive(t *testing.T) {
	m, in, serve := selScrollFixture(t, proto.Frame{ID: "p3"})
	m.scrollPane(4)
	serve()
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	a1Mouse(t, m, in.x+1, in.y+in.h+10, tea.MouseButtonNone, a1Motion)
	if m.selEdge != -3 || m.offset != 1 {
		t.Fatalf("edge %d offset %d", m.selEdge, m.offset)
	}
	serve()
	next, cmd := m.Update(selScrollMsg{})
	*m = next.(Model)
	if m.offset != 0 || cmd == nil {
		t.Fatalf("offset %d", m.offset)
	}
	next, cmd = m.Update(selScrollMsg{})
	*m = next.(Model)
	if cmd != nil || m.selTicking {
		t.Fatal("kept ticking at the live screen")
	}
	// Back inside, the edge is let go.
	a1Mouse(t, m, in.x+2, in.y+2, tea.MouseButtonNone, a1Motion)
	if m.selEdge != 0 || m.sel.by != 2 {
		t.Fatalf("back inside: edge %d sel %+v", m.selEdge, *m.sel)
	}
}

// The wheel, turned while the button is held, scrolls the history under
// the drag instead of being lost.
func TestWheelWhileDragging(t *testing.T) {
	m, in, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	a1Mouse(t, m, in.x, in.y+3, a1Left, a1Press)
	a1Mouse(t, m, in.x+4, in.y+1, tea.MouseButtonNone, a1Motion)
	a1Mouse(t, m, in.x+4, in.y+1, a1WheelUp, a1Press)
	if m.offset != 3 || m.sel == nil || !m.sel.dragging || m.sel.ay != 6 || m.sel.by != 1 {
		t.Fatalf("offset %d sel %+v", m.offset, m.sel)
	}
	a1Mouse(t, m, in.x+4, in.y+1, a1WheelDown, a1Press)
	if m.offset != 0 || m.sel.ay != 3 {
		t.Fatalf("wheel down: offset %d sel %+v", m.offset, *m.sel)
	}

	// Without history (a full-screen program) the wheel is ignored rather
	// than moving the text under the selection.
	m.frame.History, m.frame.AltScreen = 0, true
	if cmd := a1Mouse(t, m, in.x+4, in.y+1, a1WheelUp, a1Press); cmd != nil || m.sel == nil || !m.sel.dragging {
		t.Fatal("the wheel went somewhere without history")
	}
}

// An agent that takes the mouse: a drag started in it scrolls past the
// edge the same way, and none of it reaches the agent.
func TestAgentDragPastTheTop(t *testing.T) {
	m, in, serve := selScrollFixture(t, proto.Frame{ID: "p1", Mouse: true, AltScreen: true})
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	if m.click == nil {
		t.Fatal("the press was not held back")
	}
	a1Mouse(t, m, in.x+1, in.y-1, tea.MouseButtonNone, a1Motion)
	if m.offset != 1 {
		t.Fatalf("offset %d", m.offset)
	}
	serve()
	lastClipboard = ""
	a2Run(a1Mouse(t, m, in.x+1, in.y-1, a1Left, a1Release))
	if m.click != nil || !strings.Contains(lastClipboard, "\n") {
		t.Fatalf("click %v copied %q", m.click, lastClipboard)
	}
}

// A frame still catching up with a scroll is not remembered: its rows are
// not where the selection has moved to.
func TestStaleFrameIsNotRemembered(t *testing.T) {
	m, in, serve := selScrollFixture(t, proto.Frame{ID: "p3"})
	a1Mouse(t, m, in.x, in.y, a1Left, a1Press)
	want := m.sel.rows[0]
	m.scrollPane(3)
	stale := proto.Frame{ID: "p3", Lines: []string{"stale"}, History: 60}
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneFrame, stale))
	if m.sel.rows[3] != want || m.sel.rows[0] == "stale" {
		t.Fatalf("rows %q / %q", m.sel.rows[3], m.sel.rows[0])
	}
	serve()
}

// Text on a page — here the Branches page — is selected by dragging and
// copied on release; a click that does not drag still clicks.
func TestSelectOnAPage(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, sectionID(localMachine, "r1", "branches"))
	if m.tab().focused().view.Kind != kindBranches {
		t.Fatalf("shows %+v", m.tab().focused().view)
	}
	in := m.focusedRect()
	lastClipboard = ""
	a1Mouse(t, m, in.x, in.y, a1Left, a1Press)
	if m.sel == nil || m.sel.leaf != m.tab().focus || m.focus != focusMain {
		t.Fatalf("press: %+v", m.sel)
	}
	a1Mouse(t, m, in.x+7, in.y, tea.MouseButtonNone, a1Motion)
	// Drawn over the page, and no wider than it.
	for _, l := range m.leafLines(m.tab().focused(), in.w, in.h, true) {
		if ansi.StringWidth(l) > in.w {
			t.Fatalf("highlighted line too wide: %q", l)
		}
	}
	a2Run(a1Mouse(t, m, in.x+7, in.y, a1Left, a1Release))
	if lastClipboard != "Branches" {
		t.Fatalf("copied %q", lastClipboard)
	}
	if m.cursor != sectionID(localMachine, "r1", "branches") {
		t.Fatalf("a drag clicked: cursor %q", m.cursor)
	}

	// A drag that leaves the page is clamped to it, over the tree too.
	a1Mouse(t, m, in.x, in.y+1, a1Left, a1Press)
	a1Mouse(t, m, 2, in.y+1, tea.MouseButtonNone, a1Motion)
	if m.sel.bx != 0 || m.sel.by != 1 {
		t.Fatalf("over the tree: %+v", *m.sel)
	}
	// The wheel lets it go: the page moves under it.
	a1Mouse(t, m, in.x+2, in.y+1, a1WheelDown, a1Press)
	if m.sel != nil || m.click != nil {
		t.Fatal("the wheel kept a page selection")
	}

	// Pressed and released in one place: the branch on that line opens.
	a1Mouse(t, m, in.x+4, in.y+branchesPageHeader, a1Left, a1Press)
	a2Run(a1Mouse(t, m, in.x+4, in.y+branchesPageHeader, a1Left, a1Release))
	if v := m.tab().focused().view; m.sel != nil || v.Kind != kindBranch || m.cursor != branchNodeID(localMachine, "r1", v.Branch) {
		t.Fatalf("click did not open a branch: cursor %q view %+v sel %v", m.cursor, v, m.sel)
	}
}

// A page selection whose split closed mid-drag is dropped, not copied.
func TestPageSelectionLeafGone(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, sectionID(localMachine, "r1", "branches"))
	in := m.focusedRect()
	a1Mouse(t, m, in.x, in.y, a1Left, a1Press)
	m.sel.leaf = 9999
	if cmd := a1Mouse(t, m, in.x+3, in.y, tea.MouseButtonNone, a1Motion); cmd != nil || m.sel != nil || m.click != nil {
		t.Fatal("a selection over a missing leaf lived on")
	}
	if got := m.pageLines(9999, in); got != nil {
		t.Fatalf("lines of a missing leaf: %q", got)
	}
}

// On a tiny screen the drag still clamps inside a one-cell pane.
func TestDragOnATinyPane(t *testing.T) {
	m, in, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	m.width, m.height = 20, 5
	in = m.focusedRect()
	a1Mouse(t, m, in.x, in.y, a1Left, a1Press)
	a1Mouse(t, m, in.x+50, in.y+50, tea.MouseButtonNone, a1Motion)
	if m.sel == nil || m.sel.bx != in.w-1 || m.sel.by != in.h-1 {
		t.Fatalf("tiny: in %+v sel %+v", in, m.sel)
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Fatalf("line wider than the screen: %q", l)
		}
	}
}
