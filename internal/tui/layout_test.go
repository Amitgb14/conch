package tui

import (
	"encoding/json"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/charmbracelet/x/ansi"
	"testing"
)

func newTestTab() (*tab, func() int) {
	id := 0
	next := func() int { id++; return id }
	return &tab{name: "1", root: &layoutNode{leaf: &leaf{id: next()}}, focus: 1}, next
}

func TestSplitLayoutAndRemove(t *testing.T) {
	tb, next := newTestTab()
	// [1 | 2], then 2 split down into [2 / 3]
	if !tb.root.split(1, splitRight, &leaf{id: next()}) || !tb.root.split(2, splitDown, &leaf{id: next()}) {
		t.Fatal("split failed")
	}
	rects := map[int]rect{}
	var bars []splitBar
	tb.root.layout(rect{0, 0, 100, 40}, rects, &bars)
	if rects[1] != (rect{0, 0, 50, 40}) || rects[2] != (rect{50, 0, 50, 20}) || rects[3] != (rect{50, 20, 50, 20}) {
		t.Fatalf("rects: %v", rects)
	}
	if len(bars) != 2 || bars[0].pos != 50 || bars[1].pos != 20 {
		t.Fatalf("bars: %+v", bars)
	}

	if id, ok := neighbor(rects, 1, 1, 0); !ok || id != 2 {
		t.Fatalf("right of 1: %d %v", id, ok)
	}
	if id, ok := neighbor(rects, 3, 0, -1); !ok || id != 2 {
		t.Fatalf("above 3: %d", id)
	}
	if id, ok := neighbor(rects, 3, -1, 0); !ok || id != 1 {
		t.Fatalf("left of 3: %d", id)
	}
	if _, ok := neighbor(rects, 1, -1, 0); ok {
		t.Fatal("found a leaf left of the leftmost")
	}

	// Extreme ratios still leave both sides usable.
	tb.root.ratio = 0.01
	rects = map[int]rect{}
	bars = nil
	tb.root.layout(rect{0, 0, 100, 40}, rects, &bars)
	if rects[1].w < minLeaf {
		t.Fatalf("squeezed to %d", rects[1].w)
	}

	tb.root = tb.root.remove(2)
	if ls := tb.root.leaves(); len(ls) != 2 || ls[0].id != 1 || ls[1].id != 3 {
		t.Fatalf("after remove: %v", ls)
	}
	tb.root = tb.root.remove(1)
	if tb.root.dir != splitNone || tb.root.leaf.id != 3 {
		t.Fatal("sibling did not take the parent's place")
	}
	if tb.root.remove(3) != nil {
		t.Fatal("removing the last leaf should empty the tab")
	}
}

func TestLayoutPersistence(t *testing.T) {
	tb, next := newTestTab()
	tb.root.leaf.view = viewRef{Row: "pane:p1", Kind: kindPane, Machine: localMachine, PaneID: "p1"}
	nl := &leaf{id: next(), view: viewRef{Row: "b:r1:main", Kind: kindBranch, Machine: localMachine, ProjectID: "r1", Branch: "main"}}
	tb.root.split(1, splitDown, nl)
	tb.root.ratio = 0.3
	tb.focus = nl.id

	b, _ := json.Marshal(savedTab{Name: "work", Root: saveNode(tb.root, tb.focus)})
	var st savedTab
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	id := 100
	root, focus, err := loadNode(st.Root, func() int { id++; return id })
	if err != nil {
		t.Fatal(err)
	}
	ls := root.leaves()
	if root.dir != splitDown || root.ratio != 0.3 || len(ls) != 2 || ls[0].view.PaneID != "p1" || ls[1].view.Branch != "main" || focus != ls[1].id {
		t.Fatalf("restored: dir %v ratio %v leaves %+v %+v focus %d", root.dir, root.ratio, ls[0].view, ls[1].view, focus)
	}
}

func splitModel() *Model {
	panes := []proto.PaneInfo{{ID: "p1", State: proto.PaneRunning}, {ID: "p2", State: proto.PaneRunning}, {ID: "p3", State: proto.PaneRunning}}
	m := &Model{width: 160, height: 40, expanded: map[string]bool{}, showAll: map[string]bool{},
		frames: map[string]*proto.Frame{}, subscribed: map[string]bool{},
		machines: []*machine{{id: localMachine, label: "local", panes: panes, sizes: map[string][2]int{}}}}
	for _, p := range panes {
		m.rows = append(m.rows, row{id: paneNodeID(localMachine, p.ID), kind: kindPane, machine: localMachine, paneID: p.ID})
	}
	return m
}

func TestTreeClickKeepsSplits(t *testing.T) {
	m := splitModel()
	// A single-leaf tab previews what is opened.
	m.cursor = m.rows[0].id
	m.show(m.rows[0])
	if got := m.tab().focused().view.PaneID; got != "p1" {
		t.Fatalf("single leaf shows %q", got)
	}
	// Split, then fill the new half: it waits for a pick instead of
	// mirroring p1 (split with a view other than the focused one).
	m.split(splitRight, viewOf(m.rows[1]))
	if ls := m.tab().root.leaves(); len(ls) != 2 || ls[0].view.PaneID != "p1" || ls[1].view.PaneID != "p2" {
		t.Fatalf("split: %+v", ls)
	}
	// Opening p3 must not replace either half: it gets its own tab.
	m.cursor = m.rows[2].id
	m.show(m.rows[2])
	if len(m.tabs) != 2 || m.activeTab != 1 || m.tab().focused().view.PaneID != "p3" {
		t.Fatalf("p3 should open in a new tab: tabs %d active %d", len(m.tabs), m.activeTab)
	}
	if ls := m.tabs[0].root.leaves(); ls[0].view.PaneID != "p1" || ls[1].view.PaneID != "p2" {
		t.Fatalf("the split was changed: %+v %+v", ls[0].view, ls[1].view)
	}
	// Opening p1 again jumps to the split that shows it.
	m.show(m.rows[0])
	if m.activeTab != 0 || m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("p1 should focus its existing view, tab %d", m.activeTab)
	}
}

func TestNewTabWaitsForAPick(t *testing.T) {
	m := splitModel()
	m.rows = append(m.rows, row{id: "p:r1", kind: kindProject, machine: localMachine, projectID: "r1"})
	m.cursor = "p:r1"
	m.show(m.rows[3]) // a project page, not a terminal
	m.newTab(viewRef{})
	f := m.tab().focused()
	if len(m.tabs) != 2 || !f.pick || !f.view.empty() {
		t.Fatalf("new tab copied the selection: %+v", f.view)
	}
	m.syncView() // the cursor still on the project must not fill it
	if !m.tab().focused().view.empty() {
		t.Fatal("an empty tab followed the tree cursor")
	}
	m.cursor = m.rows[1].id
	m.show(m.rows[1])
	if got := m.tab().focused(); got.view.PaneID != "p2" || got.pick {
		t.Fatalf("opening a row fills the empty tab: %+v", got)
	}
}

func TestNoDuplicatesAndStableTabNames(t *testing.T) {
	m := splitModel()
	m.machines[0].panes[0].Name, m.machines[0].panes[1].Name, m.machines[0].panes[2].Name = "claude", "zsh", "codex"
	m.cursor = m.rows[0].id
	m.show(m.rows[0])                      // tab 1: claude
	m.split(splitRight, viewOf(m.rows[1])) // tab 1: claude | zsh, focus zsh
	if got := ansi.Strip(m.tabLabel(m.tab())); got != "claude ⊞" {
		t.Fatalf("tab named after its focused split: %q", got)
	}
	m.cursor = m.rows[2].id
	m.show(m.rows[2]) // tab 2: codex
	if m.activeTab != 1 {
		t.Fatal("codex opens in a tab of its own")
	}
	// Moving the tree cursor over claude (shown in tab 1) must not copy it
	// into tab 2.
	m.cursor = m.rows[0].id
	m.syncView()
	if got := m.tab().focused().view.PaneID; got != "codex" && got != "p3" {
		t.Fatalf("tab 2 now shows %q", got)
	}
	// Clicking it jumps to tab 1 instead.
	m.show(m.rows[0])
	if m.activeTab != 0 {
		t.Fatalf("active tab %d", m.activeTab)
	}
}

func TestClosedPaneLeavesLayout(t *testing.T) {
	m := splitModel()
	m.cursor = m.rows[0].id
	m.show(m.rows[0])
	m.split(splitRight, viewOf(m.rows[1])) // tab 1: p1 | p2
	m.cursor = m.rows[2].id
	m.show(m.rows[2]) // tab 2: p3
	m.dropPane(localMachine, "p2")
	if ls := m.tabs[0].root.leaves(); len(ls) != 1 || ls[0].view.PaneID != "p1" {
		t.Fatalf("p2's split should close: %+v", ls)
	}
	m.dropPane(localMachine, "p3")
	if len(m.tabs) != 1 || m.activeTab != 0 {
		t.Fatalf("p3's tab should close: %d tabs, active %d", len(m.tabs), m.activeTab)
	}
	m.dropPane(localMachine, "p1")
	if len(m.tabs) != 1 || !m.tabs[0].root.leaves()[0].view.empty() {
		t.Fatal("the last tab stays, empty")
	}
}

func TestEachPaneGetsItsTab(t *testing.T) {
	m := splitModel()
	m.machines[0].panes = append(m.machines[0].panes, proto.PaneInfo{ID: "p4", State: proto.PaneRunning})
	m.rows = append(m.rows,
		row{id: paneNodeID(localMachine, "p4"), kind: kindPane, machine: localMachine, paneID: "p4"},
		row{id: "p:r1", kind: kindProject, machine: localMachine, projectID: "r1"})
	open := func(i int) { m.cursor = m.rows[i].id; m.show(m.rows[i]) }
	shows := func(tab int) string { return m.tabs[tab].root.leaves()[0].view.PaneID }

	open(0) // the first tab is empty: p1 takes it
	m.split(splitRight, viewOf(m.rows[1]))
	open(2) // tab 2: p3
	open(3) // tab 3: p4, not replacing p3
	if len(m.tabs) != 3 || shows(1) != "p3" || shows(2) != "p4" {
		t.Fatalf("tabs: %d, tab 2 %q, tab 3 %q", len(m.tabs), shows(1), shows(2))
	}
	open(2) // p3 again: its tab
	if m.activeTab != 1 || len(m.tabs) != 3 {
		t.Fatalf("p3 should jump to tab 2, active %d", m.activeTab)
	}
	// Moving the cursor over agents doesn't change any tab.
	m.cursor = m.rows[3].id
	m.syncView()
	if shows(1) != "p3" {
		t.Fatal("cursor movement replaced an agent's tab")
	}
	// A project page opens in a browsing tab of its own, then follows the
	// cursor over other pages.
	open(4)
	if len(m.tabs) != 4 || m.tab().focused().view.Kind != kindProject {
		t.Fatalf("project: %d tabs", len(m.tabs))
	}
	// Closing p4 closes its tab.
	m.dropPane(localMachine, "p4")
	if len(m.tabs) != 3 {
		t.Fatalf("after closing p4: %d tabs", len(m.tabs))
	}
}

func TestOpeningShownPaneMovesIt(t *testing.T) {
	m := splitModel()
	m.cursor = m.rows[0].id
	m.show(m.rows[0])
	// O on the pane shown alone in its tab: stays one tab, no copy.
	m.newTab(viewOf(m.rows[0]))
	if len(m.tabs) != 1 {
		t.Fatalf("O copied a pane alone in its tab: %d tabs", len(m.tabs))
	}
	m.split(splitRight, viewOf(m.rows[1])) // p1 | p2
	// O on p1: it breaks out into its own tab.
	m.newTab(viewOf(m.rows[0]))
	count := 0
	for _, tb := range m.tabs {
		for _, l := range tb.root.leaves() {
			if l.view.PaneID == "p1" {
				count++
			}
		}
	}
	if count != 1 || len(m.tabs) != 2 || m.tab().focused().view.PaneID != "p1" || len(m.tabs[0].root.leaves()) != 1 {
		t.Fatalf("break out: p1 shown %d times, %d tabs", count, len(m.tabs))
	}
	// v on p1 from the p2 tab joins it back there.
	m.gotoTab(0)
	m.split(splitRight, viewOf(m.rows[0]))
	if len(m.tabs) != 1 || len(m.tab().root.leaves()) != 2 {
		t.Fatalf("join: %d tabs, %d leaves", len(m.tabs), len(m.tab().root.leaves()))
	}
}
