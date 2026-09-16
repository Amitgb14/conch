package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// ctrl+b r asks a pane's program to draw its screen again, for stale text a
// partial redraw left behind. Reported with a screenshot of an agent's input
// box holding the tail of a longer line.
func TestRedrawPaneKey(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c, p2 := a1FakeClient(t, "pane.redraw.v1")
	m.machines[0].c, m.machines[0].server = c, c.Server
	_ = peer

	// From the tree.
	a1At(t, m, paneNodeID(localMachine, "p1"))
	a2Run(a1Prefixed(t, m, runes("r")))
	p2.waitMethod(t, proto.MethodPaneRedraw, `"p1"`)
	if !strings.Contains(m.flash, "redrawing") || m.flashIsErr {
		t.Fatalf("flash: %q", m.flash)
	}

	// And while typing in the pane.
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	a2Run(a1Prefixed(t, m, runes("r")))
	if m.focus != focusMain {
		t.Fatal("redrawing should not leave the pane")
	}
	if n := p2.count(t, c, proto.MethodPaneRedraw, `"p1"`); n != 2 {
		t.Fatalf("redraws sent: %d", n)
	}

	// The row menu offers it too.
	r, _ := m.selectedRow()
	mu := newRowMenu(*m, r, 0, 0)
	found := false
	for i, it := range mu.items {
		if strings.HasPrefix(it.label, "Redraw") {
			found = true
			a2Run(mu.run(m, i))
		}
	}
	if !found {
		t.Fatal("no Redraw in the pane menu")
	}
	if n := p2.count(t, c, proto.MethodPaneRedraw, `"p1"`); n != 3 {
		t.Fatalf("after the menu: %d", n)
	}

	// Rows that are not panes, offline machines and old servers say why.
	a1At(t, m, machineID(localMachine))
	row, _ := m.selectedRow()
	if cmd := m.redrawPane(row); cmd != nil || !strings.Contains(m.flash, "agent or terminal") {
		t.Fatalf("machine row: %q", m.flash)
	}
	paneRow := m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p1"))]
	old, _ := a1FakeClient(t, "pane.v1")
	m.machines[0].c = old
	if cmd := m.redrawPane(paneRow); cmd != nil || !strings.Contains(m.flash, "predates") {
		t.Fatalf("old server: %q", m.flash)
	}
	m.machines[0].c = nil
	if cmd := m.redrawPane(paneRow); cmd != nil || !strings.Contains(m.flash, "offline") {
		t.Fatalf("offline: %q", m.flash)
	}
}
