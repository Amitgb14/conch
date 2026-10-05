package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestOwnPaneIsNeverShown: a conch told to open inside a pane
// (`CONCH_PANE_ID= conch`, which its own refusal suggests) must not show
// that pane. It resizes every pane it shows to the space it has, so showing
// its own shrinks the terminal it draws in, which shrinks the space, which
// shrinks the pane — 1×1, with the frames chasing each other. Seen while
// driving a TUI inside a pane of the server it was driving.
func TestOwnPaneIsNeverShown(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.ownPane = "p1" // what pane.caller said (machine.go)
	m.rebuild()

	var own row
	for _, r := range m.rows {
		if r.kind == kindPane && r.paneID == "p1" {
			own = r
		}
	}
	if own.id == "" {
		t.Fatal("the fixture has no pane p1")
	}
	if !m.ownPaneRow(own) {
		t.Fatal("it does not know its own pane")
	}

	// Showing it does nothing but say why.
	before := m.tab().focused().view.Row
	m.show(own)
	if got := m.tab().focused().view.Row; got != before {
		t.Fatalf("it opened itself: %q", got)
	}
	if !strings.Contains(m.flash, "this conch") {
		t.Errorf("it said %q", m.flash)
	}

	// Another pane still opens, so the guard is about that one pane.
	for _, r := range m.rows {
		if r.kind == kindPane && r.paneID != "p1" {
			m.show(r)
			if m.tab().focused().view.Row != r.id {
				t.Fatalf("it refused %s too", r.paneID)
			}
			break
		}
	}
}

// TestOwnPaneIsNeverResized: the belt to the braces. Even with the pane on
// screen somehow, no resize is sent for it — that is the half that shrinks
// the terminal.
func TestOwnPaneIsNeverResized(t *testing.T) {
	m, peer := a1Fixture(t, true)
	mach := m.machines[0]
	m.width, m.height = 120, 40
	m.rebuild()

	// With the pane shown and nothing claimed about who we are, conch
	// resizes it, as it does any pane it draws.
	own := paneNodeID(localMachine, "p1")
	a1Open(t, m, own)
	peer.waitMethod(t, proto.MethodPaneResize, `"id":"p1"`)

	// Told that it is our own, it is taken off the screen and nothing more
	// is sent for it — the resize is the half that shrinks the terminal.
	seen := len(peer.snapshot())
	mach.sizes = map[string][2]int{}
	mach.ownPane = "p1"
	m.dropOwnPane(mach.id, "p1")
	m.syncView()
	if sentResize(peer, "p1", seen) {
		t.Fatal("it resized its own pane")
	}
	if m.tab().focused().view.Row == own {
		t.Fatal("its own pane is still on screen")
	}

	// And if it is on screen anyway — put there by something that did not
	// go through show — the resize is still not sent. That is the half
	// that shrinks the terminal, so it is guarded on its own.
	seen = len(peer.snapshot())
	mach.sizes = map[string][2]int{}
	i := indexOfRow(m.rows, own)
	if i < 0 {
		t.Fatalf("no row %q", own)
	}
	m.assign(m.tab().focused(), m.rows[i])
	m.syncView()
	if sentResize(peer, "p1", seen) {
		t.Fatal("it resized its own pane after being handed it directly")
	}
	if m.subscribed[paneKey(localMachine, "p1")] {
		t.Error("it subscribed to its own pane")
	}
}

// sentResize reports whether a resize for that pane was sent after the
// first `from` messages.
func sentResize(peer *a1Peer, pane string, from int) bool {
	msgs := peer.snapshot()
	if from > len(msgs) {
		from = len(msgs)
	}
	for _, msg := range msgs[from:] {
		if msg.Method != proto.MethodPaneResize {
			continue
		}
		var p proto.PaneResizeParams
		if json.Unmarshal(msg.Params, &p) == nil && p.ID == pane {
			return true
		}
	}
	return false
}
