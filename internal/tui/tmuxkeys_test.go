package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
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
	m.tab().sync = true
	saved := m.savedTabs()
	var back Model
	back.restoreTabs(saved, 0)
	if !back.tab().sync {
		t.Fatal("sync not saved")
	}
}
