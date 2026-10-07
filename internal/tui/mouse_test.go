package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

const (
	a1Left      = tea.MouseButtonLeft
	a1Right     = tea.MouseButtonRight
	a1WheelUp   = tea.MouseButtonWheelUp
	a1WheelDown = tea.MouseButtonWheelDown
	a1Press     = tea.MouseActionPress
	a1Release   = tea.MouseActionRelease
	a1Motion    = tea.MouseActionMotion
)

// a1RowY is the screen line of tree row id.
func a1RowY(t *testing.T, m *Model, id string) int {
	t.Helper()
	i := indexOfRow(m.rows, id)
	if i < 0 {
		t.Fatalf("no row %s", id)
	}
	return 2 + i - m.scroll
}

func TestA1MouseTabBar(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	a1Open(t, m, paneNodeID(localMachine, "p4"))
	a1At(t, m, cliID(localMachine))                 // lists both tabs
	m.machines[0].panes[2].State = proto.PaneExited // nothing to confirm on close
	m.machines[0].panes[3].State = proto.PaneExited
	mr := m.mainRect()
	hitX := func(kind int) int {
		t.Helper()
		_, hits := m.tabBar(mr.w)
		for _, h := range hits {
			if h.tab == kind {
				return mr.x + h.x0
			}
		}
		t.Fatalf("no hit %d in %+v", kind, hits)
		return 0
	}
	// Clicking the first tab switches to it; the cursor stays in the tree.
	a1Mouse(t, m, hitX(0), 0, a1Left, a1Press)
	if m.activeTab != 0 {
		t.Fatalf("tab click: active %d", m.activeTab)
	}
	// A release or right button on the bar does nothing.
	a1Mouse(t, m, hitX(1), 0, a1Left, a1Release)
	a1Mouse(t, m, hitX(1), 0, a1Right, a1Press)
	if m.activeTab != 0 {
		t.Fatal("non-left click switched tabs")
	}
	// + opens the New menu under it; its first item opens an empty tab.
	a1Mouse(t, m, hitX(-1), 0, a1Left, a1Press)
	mu, ok := m.overlay.(*menu)
	if !ok || len(m.tabs) != 2 {
		t.Fatalf("+: overlay %T, %d tabs", m.overlay, len(m.tabs))
	}
	b := mu.render(*m)
	a1Mouse(t, m, b.x+2, b.y+1, a1Left, a1Press)
	if m.overlay != nil || len(m.tabs) != 3 || !m.tab().focused().pick {
		t.Fatalf("empty tab item: overlay %T, %d tabs", m.overlay, len(m.tabs))
	}
	// × closes the active tab (nothing running in it).
	a1Mouse(t, m, hitX(-2), 0, a1Left, a1Press)
	if len(m.tabs) != 2 {
		t.Fatalf("×: %d tabs", len(m.tabs))
	}
	// Clicking empty bar space does nothing.
	a1Mouse(t, m, m.width-2, 0, a1Left, a1Press)
	if len(m.tabs) != 2 {
		t.Fatal("empty bar space acted")
	}
}

func TestA1MouseSplitBorderDrag(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	_, bars := m.leafRects()
	if len(bars) != 1 {
		t.Fatalf("bars %d", len(bars))
	}
	bar := bars[0]
	a1Mouse(t, m, bar.pos, 10, a1Left, a1Press)
	if m.barDrag == nil {
		t.Fatal("press on the border did not start a drag")
	}
	a1Mouse(t, m, bar.area.x+bar.area.w/4, 10, tea.MouseButtonNone, a1Motion)
	if r := m.tab().root.ratio; r < 0.24 || r > 0.26 {
		t.Fatalf("dragged ratio %v", r)
	}
	a1Mouse(t, m, bar.area.x+1, 10, tea.MouseButtonNone, a1Motion) // clamps
	if r := m.tab().root.ratio; r != 0.05 {
		t.Fatalf("clamped ratio %v", r)
	}
	a1Mouse(t, m, bar.pos+5, 10, a1Left, a1Press) // other actions while dragging are ignored
	if m.barDrag == nil {
		t.Fatal("drag ended on a press")
	}
	a1Mouse(t, m, bar.pos, 10, a1Left, a1Release)
	if m.barDrag != nil {
		t.Fatal("release did not end the drag")
	}

	// A stacked split drags vertically.
	m.split(splitDown, viewRef{})
	_, bars = m.leafRects()
	var down splitBar
	for _, b := range bars {
		if b.node.dir == splitDown {
			down = b
		}
	}
	x := down.area.x + down.area.w/2
	a1Mouse(t, m, x, down.pos-1, a1Left, a1Press)
	if m.barDrag == nil {
		t.Fatal("press on the horizontal border did not start a drag")
	}
	a1Mouse(t, m, x, down.area.y+down.area.h*3/4, tea.MouseButtonNone, a1Motion)
	if r := down.node.ratio; r < 0.7 || r > 0.8 {
		t.Fatalf("vertical ratio %v", r)
	}
	a1Mouse(t, m, x, 0, a1Left, a1Release)

	// Clicking inside the other leaf focuses it.
	rects, _ := m.leafRects()
	firstLeaf := m.tab().root.leaves()[0].id
	r := rects[firstLeaf]
	a1Mouse(t, m, r.x+3, r.y+3, a1Left, a1Press)
	if m.tab().focus != firstLeaf || m.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("click focus: %d cursor %s", m.tab().focus, m.cursor)
	}
}

func TestA1MouseSidebar(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// Row click shows a pane in a tab; the tree keeps focus.
	p3 := paneNodeID(localMachine, "p3")
	a1Mouse(t, m, 10, a1RowY(t, m, p3), a1Left, a1Press)
	if m.cursor != p3 || len(m.tabs) != 1 || m.focus != focusSidebar {
		t.Fatalf("row click: cursor %s tabs %d", m.cursor, len(m.tabs))
	}
	// A second click soon after activates it.
	a1Mouse(t, m, 10, a1RowY(t, m, p3), a1Left, a1Press)
	if m.focus != focusMain {
		t.Fatal("double click did not focus the pane")
	}
	// Clicking a folder's arrow folds it; clicking a selected folder toggles.
	proj := projectNodeID(localMachine, "r1")
	a1Mouse(t, m, 5, a1RowY(t, m, proj), a1Left, a1Press) // depth 2: the arrow is at 5
	if m.expanded[proj] {
		t.Fatal("expander click did not fold")
	}
	a1Mouse(t, m, 12, a1RowY(t, m, proj), a1Left, a1Press)
	if !m.expanded[proj] {
		t.Fatal("click on the selected folder did not open it")
	}
	// Right click opens the row menu.
	a1Mouse(t, m, 12, a1RowY(t, m, proj), a1Right, a1Press)
	if _, ok := m.overlay.(*menu); !ok {
		t.Fatalf("right click: %T", m.overlay)
	}
	// With an overlay, the mouse goes to it (a menu closes on an outside
	// click or ignores it; either way the tree is untouched).
	cursor := m.cursor
	a1Mouse(t, m, 12, a1RowY(t, m, p3), a1Left, a1Release)
	if m.cursor != cursor {
		t.Fatal("mouse reached the tree under an overlay")
	}
	m.overlay = nil
	// The header line starts filtering; line 0 (the border) and below the
	// rows do nothing; releases do nothing.
	a1Mouse(t, m, 5, 1, a1Left, a1Press)
	if !m.filtering {
		t.Fatal("header click did not start filtering")
	}
	m.filtering = false
	a1Mouse(t, m, 5, 0, a1Left, a1Press)
	a1Mouse(t, m, 5, 2+len(m.rows)+3, a1Left, a1Press)
	a1Mouse(t, m, 5, a1RowY(t, m, p3), a1Left, a1Release)
	if m.filtering || m.cursor != cursor {
		t.Fatal("clicks off the rows acted")
	}
	// "… more" lists every branch.
	m.rows = append(m.rows, row{id: moreID(localMachine, "r1"), kind: kindMore, depth: 3, machine: localMachine, projectID: "r1"})
	a1Mouse(t, m, 12, 2+len(m.rows)-1, a1Left, a1Press)
	if !m.showAll["r1"] {
		t.Fatal("more click")
	}
}

func TestA1MouseSidebarWheelAndResize(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.height = 8 // 5 tree lines for 14 rows
	a1Mouse(t, m, 5, 4, a1WheelDown, a1Press)
	if m.scroll != 3 {
		t.Fatalf("wheel down: %d", m.scroll)
	}
	for i := 0; i < 5; i++ {
		a1Mouse(t, m, 5, 4, a1WheelDown, a1Press)
	}
	if want := len(m.rows) - m.sidebarRowsVisible(); m.scroll != want {
		t.Fatalf("wheel clamps at %d, got %d", want, m.scroll)
	}
	a1Mouse(t, m, 5, 4, a1WheelUp, a1Press)
	if m.scroll != len(m.rows)-m.sidebarRowsVisible()-3 {
		t.Fatalf("wheel up: %d", m.scroll)
	}

	// Dragging the sidebar's edge resizes it within limits.
	m.height = 40
	a1Mouse(t, m, m.sidebarW-1, 5, a1Left, a1Press)
	if !m.dragging {
		t.Fatal("edge press did not start a drag")
	}
	a1Mouse(t, m, 49, 5, tea.MouseButtonNone, a1Motion)
	if m.sidebarW != 50 {
		t.Fatalf("sidebar %d", m.sidebarW)
	}
	a1Mouse(t, m, 150, 5, tea.MouseButtonNone, a1Motion)
	if m.sidebarW != maxSidebarWidth {
		t.Fatalf("sidebar max %d", m.sidebarW)
	}
	a1Mouse(t, m, 1, 5, tea.MouseButtonNone, a1Motion)
	if m.sidebarW != minSidebarWidth {
		t.Fatalf("sidebar min %d", m.sidebarW)
	}
	a1Mouse(t, m, 1, 5, a1Left, a1Press) // ignored while dragging
	a1Mouse(t, m, 1, 5, a1Left, a1Release)
	if m.dragging {
		t.Fatal("release did not end the drag")
	}
}

func TestA1MouseStatusBar(t *testing.T) {
	m, _ := a1Fixture(t, false)
	_, hits := m.layoutStatus()
	// The last left hint is "? keys".
	var keysX = -1
	for _, h := range hits {
		x := h.x0
		probe := *m
		probe.overlay = nil
		if cmd := h.act(&probe); cmd == nil {
			if _, ok := probe.overlay.(*help); ok {
				keysX = x
				break
			}
		}
	}
	if keysX < 0 {
		t.Fatal("no ? hint found")
	}
	a1Mouse(t, m, keysX, m.height-1, a1Left, a1Release) // not a press
	if m.overlay != nil {
		t.Fatal("release on the status bar acted")
	}
	a1Mouse(t, m, keysX, m.height-1, a1Left, a1Press)
	if _, ok := m.overlay.(*help); !ok {
		t.Fatalf("status click: %T", m.overlay)
	}
	m.overlay = nil
	if m.clickStatus(-5) != nil || m.overlay != nil {
		t.Fatal("click off every item")
	}
}

func TestA1MousePane(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c := m.machines[0].c
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	lines := []string{"alpha beta gamma", "second line here", "third"}
	m.frames[paneKey(localMachine, "p3")] = &proto.Frame{ID: "p3", Lines: lines, History: 100}
	m.syncView()
	in := m.focusedRect()
	at := func(x, y int, b tea.MouseButton, a tea.MouseAction) tea.Cmd {
		return a1Mouse(t, m, in.x+x, in.y+y, b, a)
	}

	// The wheel scrolls history.
	at(1, 1, a1WheelUp, a1Press)
	if m.offset != 3 {
		t.Fatalf("wheel up: offset %d", m.offset)
	}
	at(1, 1, a1WheelDown, a1Press)
	if m.offset != 0 {
		t.Fatalf("wheel down: offset %d", m.offset)
	}
	// Dragging selects and copies on release (the copy is not run).
	at(0, 0, a1Left, a1Press)
	if m.focus != focusMain || m.sel == nil || !m.sel.dragging {
		t.Fatal("press did not start a selection")
	}
	at(4, 0, tea.MouseButtonNone, a1Motion)
	if !m.sel.hasContent || m.sel.bx != 4 {
		t.Fatalf("drag: %+v", *m.sel)
	}
	// Leaving the pane keeps the drag going, clamped to its edge.
	a1Mouse(t, m, in.x+in.w+10, in.y+1, tea.MouseButtonNone, a1Motion)
	if m.sel.bx != in.w-1 || m.sel.by != 1 {
		t.Fatalf("drag outside: %+v", *m.sel)
	}
	if cmd := at(4, 1, a1Left, a1Release); cmd == nil {
		t.Fatal("release did not copy")
	}
	// A click without moving selects nothing.
	at(2, 2, a1Left, a1Press)
	m.lastClickID = "" // not a double click next time
	if cmd := at(2, 2, a1Left, a1Release); cmd != nil || m.sel != nil {
		t.Fatal("a plain click copied")
	}
	// A double click selects the word under the pointer.
	at(7, 0, a1Left, a1Press)
	at(7, 0, a1Left, a1Release)
	if cmd := at(7, 0, a1Left, a1Press); cmd == nil || m.sel == nil || m.sel.ax != 6 || m.sel.bx != 9 {
		t.Fatalf("double click: %+v", m.sel)
	}
	m.sel = nil

	// Full-screen programs get arrow keys for the wheel.
	m.frame.AltScreen = true
	at(1, 1, a1WheelUp, a1Press)
	peer.waitMethod(t, proto.MethodPaneSendKeys, `["up","up","up"]`)
	at(1, 1, a1WheelDown, a1Press)
	peer.waitMethod(t, proto.MethodPaneSendKeys, `["down","down","down"]`)
	// A trackpad's burst sends them one at a time after the first notch.
	burst := m.lastWheel.Add(10 * time.Millisecond)
	restore := wheelClock
	wheelClock = func() time.Time { return burst }
	at(1, 1, a1WheelUp, a1Press)
	peer.waitMethod(t, proto.MethodPaneSendKeys, `["up"]`)
	wheelClock = restore
	m.frame.AltScreen = false

	// Programs that asked for the mouse get its events.
	m.frame.Mouse = true
	at(3, 2, a1Left, a1Press)
	peer.waitMethod(t, proto.MethodPaneSendMouse, `"x":3,"y":2`)
	// ...unless alt is held: then the press is held back to see whether it
	// starts a selection.
	next, _ := m.handleMouse(tea.MouseMsg{X: in.x + 1, Y: in.y + 1, Button: a1Left, Action: a1Press, Alt: true})
	*m = next.(Model)
	if m.click == nil || m.sel == nil || !m.sel.dragging {
		t.Fatal("alt+press over a mouse program should hold the click")
	}
	if n := peer.count(t, c, proto.MethodPaneSendMouse, `"x":1,"y":1`); n != 0 {
		t.Fatalf("held press forwarded early: %d", n)
	}
	m.click, m.sel = nil, nil

	// A press on the pane's border focuses the pane.
	m.focus = focusSidebar
	a1Mouse(t, m, in.x-1, in.y+2, a1Left, a1Press)
	if m.focus != focusMain {
		t.Fatal("border click did not focus the pane")
	}
}

func TestA1MouseAgentPaneSelectsOverApp(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c := m.machines[0].c
	a1Open(t, m, paneNodeID(localMachine, "p1")) // an agent
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: []string{"hello there"}, Mouse: true}
	m.syncView()
	if !m.selectsOverApp("p1") || m.selectsOverApp("p3") {
		t.Fatal("selectsOverApp")
	}
	in := m.focusedRect()
	a1Mouse(t, m, in.x, in.y, a1Left, a1Press)
	a1Mouse(t, m, in.x+4, in.y, tea.MouseButtonNone, a1Motion)
	if cmd := a1Mouse(t, m, in.x+4, in.y, a1Left, a1Release); cmd == nil {
		t.Fatal("a drag over an agent should copy")
	}
	if n := peer.count(t, c, proto.MethodPaneSendMouse, ""); n != 0 {
		t.Fatalf("a drag reached the agent %d times", n)
	}
	// Motion without a drag goes to the program.
	a1Mouse(t, m, in.x+2, in.y, tea.MouseButtonNone, a1Motion)
	peer.waitMethod(t, proto.MethodPaneSendMouse, `"action":"motion"`)
}

func TestA1MouseZoomOverlayAndOtherViews(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, branchNodeID(localMachine, "r1", "feat"))
	// Zoomed, the sidebar's columns belong to the leaf.
	m.zoom = true
	a1Mouse(t, m, 2, 5, a1Left, a1Press)
	if m.focus != focusMain {
		t.Fatal("click on a zoomed branch view did not focus it")
	}
	a1Mouse(t, m, 2, 5, a1WheelDown, a1Press)
	m.zoom = false
	m.focus = focusSidebar

	// A sessions leaf without its view still takes focus.
	m.tab().focused().view = viewRef{Row: "s", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"}
	m.tab().focused().sessions = nil
	in := m.focusedRect()
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	if m.focus != focusMain {
		t.Fatal("click on sessions did not focus")
	}
	// An overlay takes the mouse: the version box closes on a press.
	m.overlay = newVersionInfo()
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	if m.overlay != nil {
		t.Fatal("version box stayed open")
	}
	// A pane leaf on an offline machine ignores clicks inside.
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	in = m.focusedRect()
	if cmd := a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press); cmd != nil {
		t.Fatal("offline pane click returned a command")
	}
	if m.leafAt(map[int]rect{1: {0, 0, 2, 2}}, 5, 5) != 0 {
		t.Fatal("leafAt outside every rect")
	}
}

func TestA1ForwardMouseButtons(t *testing.T) {
	c, peer := a1FakeClient(t)
	send := func(b tea.MouseButton, a tea.MouseAction) {
		forwardMouse(c, "p1", tea.MouseMsg{Button: b, Action: a, Shift: true}, 1, 2)
	}
	send(tea.MouseButtonMiddle, a1Press)
	send(tea.MouseButtonRight, a1Release)
	send(a1WheelDown, a1Press)
	send(tea.MouseButtonNone, a1Motion)
	send(tea.MouseButtonBackward, a1Press) // not forwarded
	for _, want := range []string{`"button":"middle","action":"press"`, `"button":"right","action":"release"`,
		`"button":"wheel_down","action":"wheel"`, `"button":"none","action":"motion"`} {
		if n := peer.count(t, c, proto.MethodPaneSendMouse, want); n != 1 {
			t.Errorf("%s: %d; got %v", want, n, peer.snapshot())
		}
	}
	if n := peer.count(t, c, proto.MethodPaneSendMouse, ""); n != 4 {
		t.Fatalf("forwarded %d events, want 4", n)
	}
	_ = time.Second
}

func TestA1MouseSplitDragReleasedOverSidebarEnds(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewRef{})
	_, bars := m.leafRects()
	a1Mouse(t, m, bars[0].pos, 10, a1Left, a1Press)
	a1Mouse(t, m, 2, 10, a1Left, a1Release) // over the sidebar
	if m.barDrag != nil {
		t.Fatal("drag still active after release over the sidebar")
	}
}

func TestA1MouseClickOnAgentReachesProgram(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c := m.machines[0].c
	a1Open(t, m, paneNodeID(localMachine, "p1")) // an agent: drags select over it
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: []string{"hello"}, Mouse: true}
	m.syncView()
	in := m.focusedRect()
	a1Mouse(t, m, in.x+2, in.y+1, a1Left, a1Press)
	a1Mouse(t, m, in.x+2, in.y+1, a1Left, a1Release)
	if m.click != nil {
		t.Fatal("held click never settled")
	}
	if n := peer.count(t, c, proto.MethodPaneSendMouse, `"x":2,"y":1`); n != 2 {
		t.Fatalf("a click reached the agent %d times, want press and release", n)
	}
}

// The sidebar's right border and the main area's left border sit side by
// side. Only the first started a resize, so grabbing the visible main border
// did nothing — "mouse is not working". Either border resizes now.
func TestSidebarResizesFromEitherBorder(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.height = 40
	start := m.sidebarW

	a1Mouse(t, m, m.sidebarW, 5, a1Left, a1Press) // the main area's left border
	if !m.dragging {
		t.Fatal("pressing the main area's border did not start a resize")
	}
	a1Mouse(t, m, start+10, 5, tea.MouseButtonNone, a1Motion)
	a1Mouse(t, m, start+10, 5, a1Left, a1Release)
	if m.dragging || m.sidebarW != start+11 {
		t.Fatalf("after dragging from the main border: width %d dragging %v", m.sidebarW, m.dragging)
	}

	// The tab bar's row belongs to the tabs, not the resize.
	a1Mouse(t, m, m.sidebarW, m.mainRect().y, a1Left, a1Press)
	if m.dragging {
		t.Fatal("a press on the tab bar row started a resize")
	}
	// Neither border resizes while a split is zoomed (no sidebar is shown).
	m.zoom = true
	for _, x := range []int{m.sidebarW - 1, m.sidebarW} {
		a1Mouse(t, m, x, 5, a1Left, a1Press)
		if m.dragging {
			t.Fatalf("zoomed: a press at %d started a resize", x)
		}
	}
	m.zoom = false
	// A right click on a border isn't a resize either.
	a1Mouse(t, m, m.sidebarW, 5, a1Right, a1Press)
	if m.dragging {
		t.Fatal("a right click started a resize")
	}
}

// TestA1MouseDragTabReorders drags tabs along the bar: the order follows the
// pointer, the drag owns the mouse until it is let go, and the paths that
// must not move a tab don't.
func TestA1MouseDragTabReorders(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, cliID(localMachine)) // a group that lists tabs
	mr := m.mainRect()
	order := func() string {
		var b strings.Builder
		for _, i := range m.visibleTabs() {
			b.WriteString(m.tabs[i].root.leaves()[0].view.PaneID + " ")
		}
		return strings.TrimSpace(b.String())
	}
	tabX := func(pos int) int {
		t.Helper()
		vis := m.visibleTabs()
		if pos >= len(vis) {
			t.Fatalf("no visible tab %d of %d", pos, len(vis))
		}
		_, hits := m.tabBar(mr.w)
		for _, h := range hits {
			if h.tab == vis[pos] {
				return mr.x + h.x0 + 1
			}
		}
		t.Fatalf("no hit for tab at %d", pos)
		return 0
	}
	start := order()
	if len(strings.Fields(start)) < 2 {
		t.Fatalf("this group lists %q", start)
	}

	// Press the last tab and drag it onto the first: it lands there, and
	// every other tab keeps its order.
	last := len(m.visibleTabs()) - 1
	dragged := strings.Fields(start)[last]
	a1Mouse(t, m, tabX(last), mr.y, a1Left, a1Press)
	if !m.tabDrag {
		t.Fatal("pressing a tab did not start a drag")
	}
	a1Mouse(t, m, tabX(0), mr.y, tea.MouseButtonNone, a1Motion)
	if got := strings.Fields(order())[0]; got != dragged {
		t.Fatalf("drag to the front: %q (was %q)", order(), start)
	}
	// Motion over the tab it now is does not move it again — the reason the
	// bar doesn't flicker back and forth under a held pointer.
	was := order()
	a1Mouse(t, m, tabX(0), mr.y, tea.MouseButtonNone, a1Motion)
	if order() != was {
		t.Fatalf("a second motion moved it: %q then %q", was, order())
	}
	// Off the bar nothing moves, and the drag is still on.
	a1Mouse(t, m, tabX(0), mr.y+4, tea.MouseButtonNone, a1Motion)
	if order() != was || !m.tabDrag {
		t.Fatalf("off the bar: %q, dragging %v", order(), m.tabDrag)
	}
	// Dragging past the last tab — over the + — takes it to the end.
	_, hits := m.tabBar(mr.w)
	end := mr.x + hits[len(hits)-1].x0
	a1Mouse(t, m, end, mr.y, tea.MouseButtonNone, a1Motion)
	if got := strings.Fields(order()); got[len(got)-1] != dragged {
		t.Fatalf("drag to the end: %q", order())
	}
	// Letting go ends it, and motion afterwards moves nothing.
	a1Mouse(t, m, end, mr.y, a1Left, a1Release)
	if m.tabDrag {
		t.Fatal("release left the drag on")
	}
	was = order()
	a1Mouse(t, m, tabX(0), mr.y, tea.MouseButtonNone, a1Motion)
	if order() != was {
		t.Fatalf("motion after the release moved a tab: %q then %q", was, order())
	}
}

// TestA1MouseDragTabAlone: one tab has nowhere to go, and a preview is not a
// tab to drag at all.
func TestA1MouseDragTabAlone(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	mr := m.mainRect()
	_, hits := m.tabBar(mr.w)
	x := mr.x
	for _, h := range hits {
		if h.tab >= 0 {
			x = mr.x + h.x0
		}
	}
	a1Mouse(t, m, x, mr.y, a1Left, a1Press)
	if m.tabDrag {
		t.Fatal("a lone tab started a drag")
	}
	// Whatever arrives next, nothing moves and nothing panics.
	a1Mouse(t, m, x+20, mr.y, tea.MouseButtonNone, a1Motion)
	a1Mouse(t, m, x+20, mr.y, a1Left, a1Release)
	if len(m.tabs) != 1 {
		t.Fatalf("tabs: %d", len(m.tabs))
	}
}

// TestA1ClickCountsInAPane: one click starts a drag, two take the word,
// three take the line, and a click somewhere else starts counting again.
func TestA1ClickCountsInAPane(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.viewing, m.viewMachine = "p1", localMachine
	m.frame = &proto.Frame{ID: "p1", Lines: []string{"run go test ./internal/... now   ", "second line"}}
	press := func(x, y int) {
		m.selectMouse(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, x, y)
	}
	sel := func() (int, int, bool) {
		if m.sel == nil {
			return 0, 0, false
		}
		return m.sel.ax, m.sel.bx, m.sel.hasContent
	}

	// One: an anchor, nothing selected yet.
	press(8, 0)
	if _, _, has := sel(); has {
		t.Fatal("one click selected something")
	}
	// Two: the word under it.
	press(8, 0)
	ax, bx, has := sel()
	if !has || ansi.Cut(m.frame.Lines[0], ax, bx+1) != "test" {
		t.Fatalf("double click took %q", ansi.Cut(m.frame.Lines[0], ax, bx+1))
	}
	// Three: the whole line, without the blanks it is padded with.
	press(8, 0)
	ax, bx, has = sel()
	if !has || ax != 0 || ansi.Cut(m.frame.Lines[0], ax, bx+1) != "run go test ./internal/... now" {
		t.Fatalf("triple click took %q (%d..%d)", ansi.Cut(m.frame.Lines[0], ax, bx+1), ax, bx)
	}
	// A fourth does no harm: it keeps the line.
	press(8, 0)
	if ax, bx, _ := sel(); ax != 0 || bx == 0 {
		t.Fatalf("a fourth click: %d..%d", ax, bx)
	}
	// Clicking elsewhere starts counting from one again.
	press(3, 1)
	if _, _, has := sel(); has {
		t.Fatal("a click on another spot counted as a double")
	}
	// A line with nothing on it selects nothing, however often it is clicked.
	m.frame.Lines = []string{"      "}
	press(2, 0)
	press(2, 0)
	press(2, 0)
	if _, _, has := sel(); has {
		t.Fatal("a blank line was selected")
	}
}

// TestA1DragSplitSwaps: a split dragged by its title onto another swaps the
// two, and every way of letting go that should change nothing changes
// nothing.
func TestA1DragSplitSwaps(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.cursor = paneNodeID(localMachine, "p4")
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p4"))]))
	leaves := m.tab().root.leaves()
	if len(leaves) != 2 {
		t.Fatalf("leaves: %d", len(leaves))
	}
	shown := func() []string {
		var out []string
		for _, l := range m.tab().root.leaves() {
			out = append(out, l.view.PaneID)
		}
		return out
	}
	before := strings.Join(shown(), ",")
	rects, _ := m.leafRects()
	title := func(id int) (int, int) { return rects[id].x + 2, rects[id].y }

	// A press on a leaf's title starts the drag and focuses it.
	x, y := title(leaves[0].id)
	a1Mouse(t, m, x, y, a1Left, a1Press)
	if m.leafDrag != leaves[0].id {
		t.Fatalf("no drag started: %d", m.leafDrag)
	}
	// Letting go on the other leaf swaps them.
	ox, oy := title(leaves[1].id)
	a1Mouse(t, m, ox+1, oy+2, a1Left, a1Release)
	if got := strings.Join(shown(), ","); got == before {
		t.Fatalf("nothing swapped: %s", got)
	}
	if m.leafDrag != 0 {
		t.Fatal("the drag outlived the release")
	}
	// Letting go on itself changes nothing.
	was := strings.Join(shown(), ",")
	x, y = title(leaves[0].id)
	a1Mouse(t, m, x, y, a1Left, a1Press)
	a1Mouse(t, m, x+1, y+1, a1Left, a1Release)
	if got := strings.Join(shown(), ","); got != was {
		t.Fatalf("dropping on itself swapped: %s", got)
	}
	// Letting go outside the splits — over the tree — changes nothing.
	a1Mouse(t, m, x, y, a1Left, a1Press)
	a1Mouse(t, m, 1, 5, a1Left, a1Release)
	if got := strings.Join(shown(), ","); got != was || m.leafDrag != 0 {
		t.Fatalf("dropping outside: %s, drag %d", got, m.leafDrag)
	}
}

// TestA1DragSplitNeedsTwo: one split has nothing to swap with, and a click
// inside a pane is not a title.
func TestA1DragSplitNeedsTwo(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	rects, _ := m.leafRects()
	id := m.tab().root.leaves()[0].id
	a1Mouse(t, m, rects[id].x+2, rects[id].y, a1Left, a1Press)
	if m.leafDrag != 0 {
		t.Fatal("a lone split started a drag")
	}
	m.cursor = paneNodeID(localMachine, "p4")
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p4"))]))
	rects, _ = m.leafRects()
	id = m.tab().root.leaves()[0].id
	in := m.inner(rects[id])
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	if m.leafDrag != 0 {
		t.Fatal("a click inside a pane started a drag")
	}
}

// TestA1DragSplitShowsWhereItLands: while the button is held the split
// under the pointer is marked as the place it would go, and after the
// release both halves stay marked for a moment — a swap of two panes full
// of text is otherwise hard to see happen.
func TestA1DragSplitShowsWhereItLands(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.cursor = paneNodeID(localMachine, "p4")
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p4"))]))
	leaves := m.tab().root.leaves()
	rects, _ := m.leafRects()
	title := func(id int) (int, int) { return rects[id].x + 2, rects[id].y }

	x, y := title(leaves[0].id)
	a1Mouse(t, m, x, y, a1Left, a1Press)
	// Over itself: nothing is marked as a destination.
	a1Mouse(t, m, x+1, y+1, tea.MouseButtonNone, a1Motion)
	if m.leafDrop != 0 {
		t.Fatalf("its own split is marked as the drop: %d", m.leafDrop)
	}
	// Over the other: that one is where it would land.
	ox, oy := title(leaves[1].id)
	a1Mouse(t, m, ox+1, oy+2, tea.MouseButtonNone, a1Motion)
	if m.leafDrop != leaves[1].id {
		t.Fatalf("the drop is %d, want %d", m.leafDrop, leaves[1].id)
	}
	// Letting go marks both for a moment and clears the drag.
	cmd := a1Mouse(t, m, ox+1, oy+2, a1Left, a1Release)
	if m.leafDrag != 0 || m.leafDrop != 0 {
		t.Fatalf("the drag outlived the release: %d %d", m.leafDrag, m.leafDrop)
	}
	if !m.swapped(leaves[0].id) || !m.swapped(leaves[1].id) {
		t.Fatal("the two that swapped are not marked")
	}
	if m.swapped(0) {
		t.Fatal("a leaf that took no part is marked")
	}
	if cmd == nil {
		t.Fatal("nothing asked for the mark to be taken away again")
	}
	// The mark goes by itself, and the message that clears it does too.
	m.swapUntil = time.Now().Add(-time.Second)
	if m.swapped(leaves[0].id) {
		t.Fatal("the mark outstayed its time")
	}
	m.swapUntil = time.Now().Add(time.Minute)
	next, _ := m.Update(swapMarkExpiredMsg{})
	if mm := next.(Model); mm.swapped(leaves[0].id) {
		t.Fatal("the message did not clear the mark")
	}
}

// TestA1DragSplitOntoTabMoves: a split carried onto a tab in the bar moves
// into that tab instead of swapping with another split — the last path of
// roadmap 30 that needed a key — and every way of letting go that should
// move nothing moves nothing.
func TestA1DragSplitOntoTabMoves(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, cliID(localMachine)) // a group that lists tabs
	mr := m.mainRect()
	ids := func(ti int) []int {
		var out []int
		for _, l := range m.tabs[ti].root.leaves() {
			out = append(out, l.id)
		}
		return out
	}
	// Where each tab sits on the bar; a tab the bar has no room for is one
	// nothing can be dropped on either.
	drawn := func() map[int]int {
		out := map[int]int{}
		_, hits := m.tabBar(mr.w)
		for _, h := range hits {
			if h.tab >= 0 {
				out[h.tab] = mr.x + h.x0 + 1
			}
		}
		return out
	}
	press := func(id int) {
		t.Helper()
		rects, _ := m.leafRects()
		a1Mouse(t, m, rects[id].x+2, rects[id].y, a1Left, a1Press)
		if m.leafDrag != id {
			t.Fatalf("no drag started on %d: %d", id, m.leafDrag)
		}
	}

	src := m.activeTab
	carried, tabs := ids(src)[0], len(m.tabs)

	// Carried over the bar, the tab under the pointer is marked and no
	// split is. Where the tabs are is read after the press: taking hold of
	// a split selects it, and the bar lists the tabs of what is selected.
	press(carried)
	a1At(t, m, cliID(localMachine)) // taking hold selected the pane; the bar back on its group
	dst, dstX := -1, 0
	for _, i := range m.visibleTabs() {
		if x, ok := drawn()[i]; ok && i != src {
			dst, dstX = i, x
			break
		}
	}
	if dst < 0 {
		t.Fatalf("one tab on the bar: %v of %d", drawn(), len(m.tabs))
	}
	landing := m.tabs[dst] // by identity: indices shift when a tab closes
	a1Mouse(t, m, dstX, mr.y, tea.MouseButtonNone, a1Motion)
	if m.tabDrop != dst || m.leafDrop != 0 {
		t.Fatalf("over tab %d: tabDrop %d, leafDrop %d", dst, m.tabDrop, m.leafDrop)
	}
	// Let go there: the split joins that tab, and the tab it was the last
	// split of goes with it.
	a1Mouse(t, m, dstX, mr.y, a1Left, a1Release)
	if m.leafDrag != 0 || m.tabDrop != tabDropNone {
		t.Fatalf("the drag outlived the release: %d, %d", m.leafDrag, m.tabDrop)
	}
	if len(m.tabs) != tabs-1 {
		t.Fatalf("%d tabs, want %d: the emptied one stayed", len(m.tabs), tabs-1)
	}
	at := slices.Index(m.tabs, landing)
	if at < 0 {
		t.Fatal("the tab it was dropped on is gone")
	}
	if !slices.Contains(ids(at), carried) {
		t.Fatalf("the split did not arrive: %v", ids(at))
	}
	if m.activeTab != at {
		t.Fatalf("active tab %d, want the one it was dropped on (%d)", m.activeTab, at)
	}
	if m.tabs[at].focus != carried {
		t.Fatalf("focus %d, want the split that moved (%d)", m.tabs[at].focus, carried)
	}

	// Onto the tab it is already in, and onto the + — a hit on the bar that
	// is no tab — nothing moves and no tab is made.
	was, tabs := slices.Clone(ids(at)), len(m.tabs)
	ownX, ok := drawn()[at]
	if !ok {
		t.Fatalf("its own tab is not on the bar: %v", drawn())
	}
	press(carried)
	a1Mouse(t, m, ownX, mr.y, a1Left, a1Release)
	if got := ids(at); !slices.Equal(got, was) || len(m.tabs) != tabs {
		t.Fatalf("dropping on its own tab moved it: %v, %d tabs", got, len(m.tabs))
	}
	plus := -1
	_, hits := m.tabBar(mr.w)
	for _, h := range hits {
		if h.tab == tabHitPlus {
			plus = mr.x + h.x0 + 1
		}
	}
	if plus < 0 {
		t.Fatal("no + on the bar")
	}
	// The only split of a tab is already a tab of its own, so the + leaves
	// it where it is rather than closing a tab to open an identical one.
	if len(ids(at)) == 1 {
		press(carried)
		a1Mouse(t, m, plus, mr.y, a1Left, a1Release)
		if got := ids(at); !slices.Equal(got, was) || len(m.tabs) != tabs {
			t.Fatalf("the + took a lone split: %v, %d tabs", got, len(m.tabs))
		}
	}

	// Dropped on the +, a split of a tab that holds more than one goes to
	// a tab of its own, beside the one it left.
	if len(ids(at)) < 2 {
		t.Fatalf("expected two splits for the +: %v", ids(at))
	}
	tabs, leaving := len(m.tabs), ids(at)[0]
	kept := m.tabs[at]
	press(leaving)
	a1Mouse(t, m, plus, mr.y, tea.MouseButtonNone, a1Motion)
	if m.tabDrop != tabHitPlus {
		t.Fatalf("over the +: tabDrop %d", m.tabDrop)
	}
	a1Mouse(t, m, plus, mr.y, a1Left, a1Release)
	if len(m.tabs) != tabs+1 {
		t.Fatalf("%d tabs, want %d: the + made none", len(m.tabs), tabs+1)
	}
	if slices.Contains(ids(slices.Index(m.tabs, kept)), leaving) {
		t.Fatal("the split stayed in the tab it left")
	}
	if ids(m.activeTab)[0] != leaving || len(ids(m.activeTab)) != 1 {
		t.Fatalf("the new tab holds %v, want just %d", ids(m.activeTab), leaving)
	}
	if m.activeTab != slices.Index(m.tabs, kept)+1 {
		t.Fatalf("the new tab is at %d, want beside the one it left", m.activeTab)
	}

	// Back where it was, for what follows.
	if cmd := m.moveLeafToTab(leaving, slices.Index(m.tabs, kept)); cmd == nil {
		t.Fatal("could not put it back")
	}
	if len(m.tabs) != tabs {
		t.Fatalf("%d tabs after putting it back, want %d", len(m.tabs), tabs)
	}

	// A tab of more than one split keeps standing when one is carried off,
	// and the split that stays takes the focus.
	if len(ids(at)) < 2 {
		t.Fatalf("expected two splits to move between: %v", ids(at))
	}
	into := -1
	for i := range m.tabs {
		if i != at {
			into = i
			break
		}
	}
	if into < 0 {
		t.Fatal("no other tab left")
	}
	keeping := m.tabs[at]
	moved, stays := ids(at)[0], ids(at)[1]
	keeping.focus = moved // the one being carried is the focused one
	if cmd := m.moveLeafToTab(moved, into); cmd == nil {
		t.Fatal("nothing moved")
	}
	if slices.Index(m.tabs, keeping) < 0 {
		t.Fatal("a tab with a split left in it closed")
	}
	if keeping.focus != stays {
		t.Fatalf("focus %d, want the split still there (%d)", keeping.focus, stays)
	}

	// What must move nothing at all.
	m.previewing = true
	if m.moveLeafToTab(stays, 0) != nil {
		t.Fatal("a preview moved a split")
	}
	m.previewing = false
	if m.moveLeafToTab(stays, len(m.tabs)) != nil {
		t.Fatal("moved into a tab past the end")
	}
	if m.moveLeafToTab(stays, -1) != nil {
		t.Fatal("moved into a tab before the first")
	}
	if m.moveLeafToTab(-1, 0) != nil {
		t.Fatal("moved a split that does not exist")
	}
	if m.moveLeafToTab(stays, m.activeTab) != nil {
		t.Fatal("moved a split into the tab it is already in")
	}

	// Zoomed there is one split on screen and no bar under the pointer, so
	// nothing is picked up.
	m.zoom = true
	rects, _ := m.leafRects()
	id := m.tab().root.leaves()[0].id
	a1Mouse(t, m, rects[id].x+2, rects[id].y, a1Left, a1Press)
	if m.leafDrag != 0 {
		t.Fatalf("a zoomed split was dragged: %d", m.leafDrag)
	}
	m.zoom = false

	// One tab holding one split: nowhere to carry it, so no drag starts —
	// the old rule, kept for the case the new one does not cover.
	for len(m.tabs) > 1 {
		m.closeTab(len(m.tabs) - 1)
	}
	tb := m.tab()
	for len(tb.root.leaves()) > 1 {
		tb.root = tb.root.remove(tb.root.leaves()[1].id)
	}
	tb.focus = tb.root.leaves()[0].id
	rects, _ = m.leafRects()
	id = tb.root.leaves()[0].id
	a1Mouse(t, m, rects[id].x+2, rects[id].y, a1Left, a1Press)
	if m.leafDrag != 0 {
		t.Fatalf("a lone split in a lone tab was dragged: %d", m.leafDrag)
	}
}

// TestA1DragTreeRowOntoTabSection: grouped by tab, a pane dragged from the
// tree onto a tab's section goes into that tab — the same move as dropping
// a split on the bar, reached from the tree, which had no drag at all.
func TestA1DragTreeRowOntoTabSection(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	m.cfg.UI.TreeGroups = "tabs"
	nm, _ := m.Update(flashExpiredMsg{}) // the tree regroups for the tabs
	*m = nm.(Model)
	m.focus = focusSidebar

	rowY := func(id string) (int, int) {
		t.Helper()
		for i, r := range m.rows {
			if r.id == id {
				return 2, i - m.scroll + 2
			}
		}
		t.Fatalf("no row %q in\n%s", id, render(m.rows))
		return 0, 0
	}
	var paneRow, tabRow row
	for _, r := range m.rows {
		if r.kind == kindTab {
			tabRow = r
			break
		}
	}
	if tabRow.id == "" {
		t.Fatalf("no tab section in\n%s", render(m.rows))
	}
	// A pane that is not already in the tab it will be dropped on.
	held := map[string]bool{}
	for _, l := range m.tabs[tabRow.tabIndex].root.leaves() {
		held[l.view.PaneID] = true
	}
	for _, r := range m.rows {
		if r.kind == kindPane && !held[r.paneID] {
			paneRow = r
			break
		}
	}
	if paneRow.id == "" {
		t.Fatalf("no pane to carry in\n%s", render(m.rows))
	}
	before := len(m.tabs[tabRow.tabIndex].root.leaves())

	// Press the row: it still selects, and a drag begins.
	x, y := rowY(paneRow.id)
	a1Mouse(t, m, x, y, a1Left, a1Press)
	if m.rowDrag != paneRow.id {
		t.Fatalf("no drag started: %q", m.rowDrag)
	}
	if m.cursor != paneRow.id {
		t.Fatalf("the press stopped selecting: %q", m.cursor)
	}
	// Over the tab's section, that section is marked.
	tx, ty := rowY(tabRow.id)
	a1Mouse(t, m, tx, ty, tea.MouseButtonNone, a1Motion)
	if m.rowDrop != tabRow.id {
		t.Fatalf("over the section: rowDrop %q", m.rowDrop)
	}
	// Let go: the pane is in that tab.
	a1Mouse(t, m, tx, ty, a1Left, a1Release)
	if m.rowDrag != "" || m.rowDrop != "" {
		t.Fatalf("the drag outlived the release: %q %q", m.rowDrag, m.rowDrop)
	}
	got := 0
	for _, l := range m.tabs[m.activeTab].root.leaves() {
		if l.view.PaneID == paneRow.paneID {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("the pane did not arrive: %d of it in the tab", got)
	}
	if len(m.tabs[m.activeTab].root.leaves()) != before+1 {
		t.Fatalf("the tab holds %d splits, want %d", len(m.tabs[m.activeTab].root.leaves()), before+1)
	}

	// The same pane onto the same section again: it is already there.
	at := m.activeTab
	was := len(m.tabs[at].root.leaves())
	if m.movePaneToTab(paneRow.machine, paneRow.paneID, at) != nil {
		t.Fatal("moved a pane into the tab it is in")
	}
	if len(m.tabs[at].root.leaves()) != was {
		t.Fatal("the tab changed anyway")
	}

	// Let go over a row that is not a tab's section, and nothing moves.
	nm, _ = m.Update(flashExpiredMsg{})
	*m = nm.(Model)
	var other row
	for _, r := range m.rows {
		if r.kind == kindBranches || r.kind == kindBranch {
			other = r
			break
		}
	}
	if other.id != "" {
		x, y = rowY(paneRow.id)
		a1Mouse(t, m, x, y, a1Left, a1Press)
		ox, oy := rowY(other.id)
		a1Mouse(t, m, ox, oy, tea.MouseButtonNone, a1Motion)
		if m.rowDrop != "" {
			t.Fatalf("a row that takes no pane was marked: %q", m.rowDrop)
		}
		a1Mouse(t, m, ox, oy, a1Left, a1Release)
		if m.rowDrag != "" {
			t.Fatal("the drag outlived a release over nothing")
		}
	}

	// A row that is not a pane, and a tab that is not there, move nothing.
	if m.dropRowOnTab(tabRow.id, tabRow.id) != nil {
		t.Fatal("a section was carried into a tab")
	}
	if m.movePaneToTab(paneRow.machine, paneRow.paneID, len(m.tabs)) != nil {
		t.Fatal("moved into a tab past the end")
	}
	if m.movePaneToTab(paneRow.machine, "", 0) != nil {
		t.Fatal("moved a pane with no id")
	}

	// Grouped the other way there is nowhere to drop one, so no drag
	// starts: the tree works exactly as it did.
	m.cfg.UI.TreeGroups = ""
	nm, _ = m.Update(flashExpiredMsg{})
	*m = nm.(Model)
	for _, r := range m.rows {
		if r.kind == kindPane {
			x, y = rowY(r.id)
			a1Mouse(t, m, x, y, a1Left, a1Press)
			if m.rowDrag != "" {
				t.Fatalf("a drag started with the grouping off: %q", m.rowDrag)
			}
			break
		}
	}
}

// TestA1ClickBranchShowsChangesNotTheGitWindow: a click on a branch opened
// the git window over the whole right-hand side, which is a lot to do for
// one click on a row — reported from use. A click shows the branch's
// changes; b or $ opens the window.
func TestA1ClickBranchShowsChangesNotTheGitWindow(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.focus = focusSidebar
	id := branchNodeID(localMachine, "r1", "feat")
	i := indexOfRow(m.rows, id)
	if i < 0 {
		t.Fatalf("no branch row in\n%s", render(m.rows))
	}

	a1Mouse(t, m, 4, i-m.scroll+2, a1Left, a1Press)
	if m.overlay != nil {
		t.Fatalf("a click on a branch opened %T", m.overlay)
	}
	if m.cursor != id {
		t.Fatalf("the click selected %q, want the branch", m.cursor)
	}
	if l := m.tab().focused(); l.view.Branch != "feat" {
		t.Fatalf("the click showed %+v, want the branch's changes", l.view)
	}

	// b opening it is what openPanel does in every git panel test, on the
	// fixture that has a server to run git for it.
}
