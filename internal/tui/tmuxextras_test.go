package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// splitsModel is a model whose active tab has n splits side by side,
// leaves 1..n, focus on the last.
func splitsModel(n int) *Model {
	local := newMachine(localMachine, "local", "")
	local.state = stateOnline
	m := &Model{cfg: config.Default(), width: 200, height: 60, sidebarW: 40, machines: []*machine{local},
		frames: map[string]*proto.Frame{}, subscribed: map[string]bool{}, expanded: map[string]bool{}, showAll: map[string]bool{}}
	first := m.newLeaf(viewRef{Row: "r1", Kind: kindCLI, Machine: localMachine})
	t := &tab{root: &layoutNode{leaf: first}, focus: first.id}
	m.tabs = []*tab{t}
	for i := 2; i <= n; i++ {
		l := m.newLeaf(viewRef{Row: "r" + itoa(i), Kind: kindCLI, Machine: localMachine})
		t.root.split(t.focus, splitRight, l)
		t.focus = l.id
	}
	m.focus = focusMain // no tree cursor picking tabs
	return m
}

func leafOrder(t *tab) string {
	var ids []string
	for _, l := range t.root.leaves() {
		ids = append(ids, l.view.Row)
	}
	return strings.Join(ids, ",")
}

func TestLayouts(t *testing.T) {
	m := splitsModel(5)
	tb := m.tab()
	focus := tb.focus
	rectsOf := func() []rect {
		rects, _ := m.leafRects()
		var out []rect
		for _, l := range tb.root.leaves() {
			out = append(out, rects[l.id])
		}
		return out
	}
	near := func(a, b int) bool { return a-b <= 2 && b-a <= 2 }

	for i, check := range []func(rs []rect) bool{
		func(rs []rect) bool { // even-horizontal: one row, equal widths
			for _, r := range rs {
				if r.y != rs[0].y || r.h != rs[0].h || !near(r.w, rs[0].w) {
					return false
				}
			}
			return true
		},
		func(rs []rect) bool { // even-vertical: one column, equal heights
			for _, r := range rs {
				if r.x != rs[0].x || r.w != rs[0].w || !near(r.h, rs[0].h) {
					return false
				}
			}
			return true
		},
		func(rs []rect) bool { // main-horizontal: a wide main on top, the rest in a row below
			for _, r := range rs[1:] {
				if r.y != rs[1].y || r.y <= rs[0].y {
					return false
				}
			}
			return rs[0].w > rs[1].w*3 && near(rs[0].h*2, int(float64(rs[0].h+rs[1].h)*mainShare*2))
		},
		func(rs []rect) bool { // main-vertical: a tall main on the left, the rest stacked right
			for _, r := range rs[1:] {
				if r.x != rs[1].x || r.x <= rs[0].x {
					return false
				}
			}
			return rs[0].h > rs[1].h*3
		},
		func(rs []rect) bool { // tiled: 5 splits in 3 columns and 2 rows
			rows := map[int]int{}
			for _, r := range rs {
				rows[r.y]++
			}
			return len(rows) == 2 && rows[rs[0].y] == 3 && rows[rs[3].y] == 2
		},
	} {
		m.prefixArmed = false
		if cmd := m.applyLayout(i); cmd == nil {
			t.Fatalf("layout %d: no command", i)
		}
		if leafOrder(tb) != "r1,r2,r3,r4,r5" || tb.focus != focus || !strings.Contains(m.flash, layoutNames[i]) {
			t.Fatalf("%s: order %s focus %d flash %q", layoutNames[i], leafOrder(tb), tb.focus, m.flash)
		}
		if rs := rectsOf(); !check(rs) {
			t.Errorf("%s: rects %v", layoutNames[i], rs)
		}
	}

	// ctrl+b space cycles from the last layout and wraps; alt+N picks one.
	next, _ := m.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // arm nothing
	*m = next.(Model)
	m.prefixArmed = true
	next, _ = m.handleMainKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	*m = next.(Model)
	if m.tab().layout != 1 || !strings.Contains(m.flash, "even-horizontal") {
		t.Fatalf("space after tiled: layout %d %q", m.tab().layout, m.flash)
	}
	m.prefixArmed = true
	next, _ = m.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4"), Alt: true})
	*m = next.(Model)
	if m.tab().layout != 4 || !strings.Contains(m.flash, "main-vertical") {
		t.Fatalf("alt+4: layout %d %q", m.tab().layout, m.flash)
	}
	if m.applyLayout(-1); m.tab().layout != 5 {
		t.Fatalf("wrap below zero: %d", m.tab().layout)
	}

	// A zoomed tab unzooms; two splits work; one split has nothing to do.
	m.zoom = true
	m.applyLayout(2)
	if m.zoom {
		t.Fatal("still zoomed")
	}
	two := splitsModel(2)
	if two.applyLayout(4); leafOrder(two.tab()) != "r1,r2" {
		t.Fatal("tiled two")
	}
	// A tab never arranged starts at the first layout.
	fresh := splitsModel(3)
	fresh.prefixArmed = true
	next, _ = fresh.handleMainKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	*fresh = next.(Model)
	if !strings.Contains(fresh.flash, "even-horizontal") {
		t.Fatalf("first space: %q", fresh.flash)
	}
	one := splitsModel(1)
	if cmd := one.applyLayout(0); cmd != nil || !strings.Contains(one.flash, "one split") {
		t.Fatalf("one split: %q", one.flash)
	}
	// Layouts survive saving: they are plain splits.
	m.applyLayout(4)
	var back Model
	back.restoreTabs([]savedTab{{Root: saveNode(m.tab().root, m.tab().focus)}}, 0)
	if back.tabs == nil { // views without real rows are dropped: check the tree shape instead
		root, _, err := loadNode(saveNode(m.tab().root, m.tab().focus), func() int { back.leafSeq++; return back.leafSeq })
		if err != nil || len(root.leaves()) != 5 {
			t.Fatalf("saved layout: %v", err)
		}
	}
}

func TestSplitNumbers(t *testing.T) {
	m := splitsModel(3)
	first := m.tab().root.leaves()[0].id

	m.prefixArmed = true
	next, cmd := m.handleMainKey(runes("q"))
	*m = next.(Model)
	if cmd == nil || !m.numbersShown() {
		t.Fatal("q didn't show numbers")
	}
	// The view draws a number in each split.
	out := ansi.Strip(m.View())
	for _, n := range []string{" 1 ", " 2 ", " 3 "} {
		if !strings.Contains(out, n) {
			t.Fatalf("no %q badge", n)
		}
	}
	for i, l := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Fatalf("line %d too wide", i)
		}
	}
	// A digit focuses that split and hides the numbers.
	next, _ = m.handleMainKey(runes("1"))
	*m = next.(Model)
	if m.tab().focus != first || m.numbersShown() {
		t.Fatalf("1: focus %d shown %v", m.tab().focus, m.numbersShown())
	}

	// A number with no split, and esc, are swallowed; other keys go on.
	m.showNumbers()
	if _, handled := m.numberKey("7"); !handled || m.numbersShown() {
		t.Fatal("7 with three splits")
	}
	m.showNumbers()
	if _, handled := m.numberKey("esc"); !handled {
		t.Fatal("esc")
	}
	m.showNumbers()
	if _, handled := m.numberKey("x"); handled || m.numbersShown() {
		t.Fatal("x should hide the numbers and go on")
	}
	if _, handled := m.numberKey("1"); handled {
		t.Fatal("digits act only while numbers show")
	}

	// The timer hides them, but not an older timer after a newer q.
	m.showNumbers()
	old := m.numbersGen
	m.showNumbers()
	next, _ = m.update(numbersDoneMsg{gen: old})
	*m = next.(Model)
	if !m.numbersShown() {
		t.Fatal("an old timer hid new numbers")
	}
	next, _ = m.update(numbersDoneMsg{gen: m.numbersGen})
	*m = next.(Model)
	if m.numbersShown() {
		t.Fatal("timer didn't hide numbers")
	}
	m.numbersUntil = time.Now().Add(-time.Second)
	if m.numbersShown() {
		t.Fatal("expired numbers shown")
	}

	// The badge never widens a line, even when narrow.
	for _, w := range []int{1, 2, 3, 5, 20} {
		lines := numberBadge([]string{"", "hello world", ""}, 9, w)
		for _, l := range lines {
			if ansi.StringWidth(l) > max(w, 3) && w >= 3 {
				t.Fatalf("width %d: %q", w, ansi.Strip(l))
			}
		}
	}
	if got := numberBadge(nil, 1, 10); got != nil {
		t.Fatal("empty content")
	}
}

func TestMoveTabs(t *testing.T) {
	m := splitsModel(1)
	for i := 2; i <= 4; i++ {
		l := m.newLeaf(viewRef{Row: "t" + itoa(i), Kind: kindCLI, Machine: localMachine})
		m.tabs = append(m.tabs, &tab{root: &layoutNode{leaf: l}, focus: l.id})
	}
	names := func() string {
		var out []string
		for _, t := range m.tabs {
			out = append(out, t.root.leaves()[0].view.Row)
		}
		return strings.Join(out, ",")
	}
	if names() != "r1,t2,t3,t4" {
		t.Fatal(names())
	}
	m.activeTab = 1 // t2
	m.moveTab(1)
	if names() != "r1,t3,t2,t4" || m.tabs[m.activeTab].root.leaves()[0].view.Row != "t2" {
		t.Fatalf("right: %s active %d", names(), m.activeTab)
	}
	m.moveTab(-1)
	m.moveTab(-1)
	if names() != "t2,r1,t3,t4" || m.activeTab != 0 {
		t.Fatalf("left twice: %s active %d", names(), m.activeTab)
	}
	if cmd := m.moveTab(-1); cmd != nil || names() != "t2,r1,t3,t4" {
		t.Fatal("moved past the left end")
	}
	m.moveTabTo(3)
	if names() != "r1,t3,t4,t2" || m.activeTab != 3 {
		t.Fatalf("to the end: %s", names())
	}
	if m.moveTab(1) != nil || m.moveTabTo(3) != nil || m.moveTabTo(9) != nil {
		t.Fatal("no-op moves returned commands")
	}

	// The keys, and the position dialog.
	m.prefixArmed = true
	next, _ := m.handleMainKey(runes("<"))
	*m = next.(Model)
	if names() != "r1,t3,t2,t4" {
		t.Fatalf("<: %s", names())
	}
	m.prefixArmed = true
	next, _ = m.handleMainKey(runes("."))
	*m = next.(Model)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "Position 1–4 in the tab bar; 3 now.") {
		t.Fatalf("dialog: %#v", m.overlay)
	}
	d.fields[0].in.SetValue(" 1 ")
	d.update(m, a2Key("enter"))
	if names() != "t2,r1,t3,t4" {
		t.Fatalf("dialog move: %s", names())
	}
	for _, bad := range []string{"0", "5", "x", ""} {
		m.openMoveTab()
		d := m.overlay.(*dialog)
		d.fields[0].in.SetValue(bad)
		d.update(m, a2Key("enter"))
		if names() != "t2,r1,t3,t4" || !strings.Contains(m.flash, "from 1 to 4") {
			t.Fatalf("bad position %q: %s %q", bad, names(), m.flash)
		}
	}
	m.prefixArmed = true
	next, _ = m.handleMainKey(runes(">"))
	*m = next.(Model)
	if names() != "r1,t2,t3,t4" {
		t.Fatalf(">: %s", names())
	}

	// A single tab, or the preview, can't move.
	single := splitsModel(1)
	if single.openMoveTab(); single.overlay != nil || !strings.Contains(single.flash, "no other tab") {
		t.Fatalf("single: %q", single.flash)
	}
	m.previewing = true
	if m.moveTab(1) != nil {
		t.Fatal("preview moved")
	}
}

// Moving a tab among the tabs of one group keeps the other groups' tabs
// where they are.
func TestMoveTabWithinGroup(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for _, id := range []string{"p1", "p3", "p2", "p4"} {
		a1Open(t, m, paneNodeID(localMachine, id))
	}
	order := func() string {
		var out []string
		for _, t := range m.tabs {
			out = append(out, t.root.leaves()[0].view.PaneID)
		}
		return strings.Join(out, ",")
	}
	if order() != "p1,p3,p2,p4" {
		t.Fatalf("tabs %s", order())
	}
	// CLI terminals and agents: p3 (bash) and p4 (codex) aren't one section;
	// at CLI both are listed. Moving p4 left passes p3 but not the api tabs.
	a1At(t, m, cliID(localMachine))
	m.activeTab = 3
	m.focus = focusSidebar
	m.moveTab(-1)
	if order() != "p1,p4,p3,p2" {
		t.Fatalf("within CLI: %s", order())
	}
}

func TestPickerLabels(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.panes[2].Cwd = "/tmp/work/scripts" // bash: no branch
	for view, want := range map[viewRef]string{
		{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "p1"}:                   "claude · feat · idle",
		{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "p3"}:                   "bash · scripts",
		{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "p4"}:                   "codex · working",
		{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "p404"}:                 "empty",
		{Row: "x", Kind: kindBranch, Machine: localMachine, ProjectID: "r1", Branch: "b"}: "changes · b",
		{Row: "x", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"}:            "sessions · api",
		{Row: "x", Kind: kindProject, Machine: localMachine, ProjectID: "r1"}:             "api",
	} {
		if got := ansi.Strip(m.pickerLabel(view)); got != want {
			t.Errorf("%+v: %q, want %q", view, got, want)
		}
	}
}
