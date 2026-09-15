package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// prefixed presses ctrl+b then key.
func prefixed(t *testing.T, m Model, key tea.KeyMsg) Model {
	t.Helper()
	m.prefixArmed = true
	next, _ := m.handleKey(key)
	return next.(Model)
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestTmuxSplitAndTabKeys(t *testing.T) {
	m := Model{cfg: config.Default(), width: 120, height: 40, sidebarW: 20}
	first0 := m.newLeaf(viewRef{})
	m.tabs = []*tab{{root: &layoutNode{leaf: first0}, focus: first0.id}}
	tb := m.tab()
	first := tb.focus
	second := m.newLeaf(viewRef{Row: "b"})
	tb.root.split(first, splitRight, second)
	tb.root.leaves()[0].view = viewRef{Row: "a"}
	m.syncView()

	// ; goes back to the split focused before.
	m = prefixed(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.tab().focus != second.id {
		t.Fatalf("right: focus %d", m.tab().focus)
	}
	m = prefixed(t, m, runes(";"))
	if m.tab().focus != first {
		t.Fatalf("; focus %d, want %d", m.tab().focus, first)
	}
	m = prefixed(t, m, runes(";"))
	if m.tab().focus != second.id {
		t.Fatalf("; again: focus %d", m.tab().focus)
	}

	// { swaps views; focus moves with the view.
	m = prefixed(t, m, runes("{"))
	ls := m.tab().root.leaves()
	if ls[0].view.Row != "b" || ls[1].view.Row != "a" || m.tab().focus != ls[0].id {
		t.Fatalf("swap: %q %q focus %d", ls[0].view.Row, ls[1].view.Row, m.tab().focus)
	}

	// ctrl+right moves the border, and repeats without the prefix.
	before := m.tab().root.ratio
	m = prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlRight})
	if m.tab().root.ratio <= before {
		t.Fatalf("ctrl+right: ratio %v → %v", before, m.tab().root.ratio)
	}
	grown := m.tab().root.ratio
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = next.(Model)
	if m.tab().root.ratio <= grown || m.prefixArmed {
		t.Fatalf("repeated alt+right: ratio %v → %v", grown, m.tab().root.ratio)
	}

	// l returns to the last tab; 0 is the tenth.
	m.tabs = append(m.tabs, &tab{root: &layoutNode{leaf: m.newLeaf(viewRef{Row: "c"})}})
	m.tabs[1].focus = m.tabs[1].root.leaf.id
	m = prefixed(t, m, runes("2"))
	if m.activeTab != 1 {
		t.Fatalf("2: tab %d", m.activeTab)
	}
	m = prefixed(t, m, runes("l"))
	if m.activeTab != 0 {
		t.Fatalf("l: tab %d", m.activeTab)
	}
	m = prefixed(t, m, runes("l"))
	if m.activeTab != 1 {
		t.Fatalf("l again: tab %d", m.activeTab)
	}
	m = prefixed(t, m, runes("0"))
	if m.activeTab != 1 {
		t.Fatalf("0 without a tenth tab moved to %d", m.activeTab)
	}

	// w lists tabs and splits; picking a split focuses it.
	m = prefixed(t, m, runes("w"))
	mu, ok := m.overlay.(*menu)
	if !ok || len(mu.items) != 4 || mu.sel != 3 {
		t.Fatalf("picker: %#v", m.overlay)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != nil || m.activeTab != 0 || m.tab().focus != m.tab().root.leaves()[1].id {
		t.Fatalf("picked: tab %d focus %d", m.activeTab, m.tab().focus)
	}
}

func TestResizeNearestBorder(t *testing.T) {
	tb, next := newTestTab()
	// [1 | [2 / 3]]: 3's nearest vertical border is the root's.
	tb.root.split(1, splitRight, &leaf{id: next()})
	tb.root.split(2, splitDown, &leaf{id: next()})
	rects := map[int]rect{}
	var bars []splitBar
	tb.root.layout(rect{0, 0, 100, 40}, rects, &bars)
	if !resize(tb.root, bars, 3, splitRight, -10) || tb.root.ratio != 0.4 {
		t.Fatalf("left: ratio %v", tb.root.ratio)
	}
	if !resize(tb.root, bars, 3, splitDown, 4) || tb.root.b.ratio != 0.6 {
		t.Fatalf("down: ratio %v", tb.root.b.ratio)
	}
	if resize(tb.root, bars, 1, splitDown, 1) {
		t.Fatal("resized leaf 1 along an axis with no border")
	}
	if !resize(tb.root, bars, 1, splitRight, -1000) || tb.root.ratio != float64(minLeaf)/100 {
		t.Fatalf("clamp: ratio %v", tb.root.ratio)
	}
}

func TestSyncNeedsPanes(t *testing.T) {
	m := Model{cfg: config.Default(), width: 120, height: 40, sidebarW: 20}
	m = prefixed(t, m, runes("S"))
	if m.tab().sync {
		t.Fatal("sync turned on in a tab without panes")
	}
	l := m.newLeaf(viewRef{Row: "pane:p1", Kind: kindPane, Machine: localMachine, PaneID: "p1"})
	m.tabs = []*tab{{root: &layoutNode{leaf: l}, focus: l.id, sync: true}}
	saved := m.savedTabs()
	var back Model
	back.restoreTabs(saved, 0)
	if !back.tab().sync {
		t.Fatal("sync not saved")
	}
}

func TestTabsFollowTreeGroup(t *testing.T) {
	agent := &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}
	panes := []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Agent: agent},
		{ID: "p2", Name: "zsh", State: proto.PaneRunning, ProjectID: "r1"},
		{ID: "p3", Name: "zsh", State: proto.PaneRunning},
	}
	m := Model{cfg: config.Default(), width: 160, height: 40, sidebarW: 30, expanded: map[string]bool{}, showAll: map[string]bool{},
		frames: map[string]*proto.Frame{}, subscribed: map[string]bool{},
		machines: []*machine{{id: localMachine, label: "local", panes: panes, sizes: map[string][2]int{}, agents: map[string]bool{},
			projects: []proto.ProjectInfo{{ID: "r1", Name: "api"}}}}}
	m.rows = []row{
		{id: machineID(localMachine), kind: kindMachine, machine: localMachine},
		{id: projectNodeID(localMachine, "r1"), kind: kindProject, machine: localMachine, projectID: "r1"},
		{id: sectionID(localMachine, "r1", "agents"), kind: kindAgents, machine: localMachine, projectID: "r1"},
		{id: paneNodeID(localMachine, "p1"), kind: kindPane, machine: localMachine, projectID: "r1", paneID: "p1"},
		{id: sectionID(localMachine, "r1", "terminals"), kind: kindTerminals, machine: localMachine, projectID: "r1"},
		{id: paneNodeID(localMachine, "p2"), kind: kindPane, machine: localMachine, projectID: "r1", paneID: "p2"},
		{id: cliID(localMachine), kind: kindCLI, machine: localMachine},
		{id: looseTerminalsID(localMachine), kind: kindTerminals, machine: localMachine},
		{id: paneNodeID(localMachine, "p3"), kind: kindPane, machine: localMachine, paneID: "p3"},
	}
	at := func(i int) { m.cursor = m.rows[i].id; m.syncView() }
	shown := func() []string {
		var out []string
		for _, i := range m.visibleTabs() {
			out = append(out, m.tabs[i].root.leaves()[0].view.PaneID)
		}
		return out
	}
	for _, i := range []int{3, 5, 8} {
		m.cursor = m.rows[i].id
		m.show(m.rows[i])
	}
	if len(m.tabs) != 3 {
		t.Fatalf("tabs: %d", len(m.tabs))
	}

	at(0)
	if got := shown(); len(got) != 0 || !m.previewing || m.tab().focused().view.Kind != kindMachine {
		t.Fatalf("machine: tabs %v, previewing %v", got, m.previewing)
	}
	at(1)
	if got := strings.Join(shown(), ","); got != "p1,p2" || !m.previewing || m.tab().focused().view.Kind != kindProject {
		t.Fatalf("project: %s previewing %v", got, m.previewing)
	}
	at(2)
	if got := strings.Join(shown(), ","); got != "p1" || m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("project agents: %s", got)
	}
	at(6)
	if got := strings.Join(shown(), ","); got != "p3" {
		t.Fatalf("CLI: %s", got)
	}
	at(5)
	if got := strings.Join(shown(), ","); got != "p2" || m.tab().focused().view.PaneID != "p2" {
		t.Fatalf("a terminal: %s", got)
	}

	// n and p stay within the listed tabs.
	at(1)
	m = prefixed(t, m, runes("n"))
	first := m.tab().focused().view.PaneID
	m = prefixed(t, m, runes("n"))
	if second := m.tab().focused().view.PaneID; first == second || second == "p3" || first == "p3" {
		t.Fatalf("n left the project: %s then %s", first, second)
	}

	// A redraw doesn't move away from a tab picked from another group.
	at(0)
	m.gotoTab(2)
	m.syncView()
	if m.activeTab != 2 || m.previewing {
		t.Fatalf("picked tab lost: active %d previewing %v", m.activeTab, m.previewing)
	}
}
