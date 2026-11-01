package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	mach.sizes = map[string]paneSize{}
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
	mach.sizes = map[string]paneSize{}
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

// TestOwnPaneIsNeverPreviewed: moving the cursor onto this conch's own row
// previewed it — pickTab's showPreview had no check — and enter then put
// the keyboard there. R63's TUI locked up just so.
func TestOwnPaneIsNeverPreviewed(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.ownPane = "p3" // the last row of the tree
	m.rebuild()
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.cursor != paneNodeID(localMachine, "p3") {
		t.Fatalf("cursor on %q", m.cursor)
	}
	if got := m.tab().focused().view.Row; got == m.cursor {
		t.Fatal("the cursor on its own row previewed it")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.tab().focused().view.Row; got == m.cursor {
		t.Fatal("enter showed it")
	}
	if !strings.Contains(m.flash, "this conch") {
		t.Errorf("it said %q", m.flash)
	}
	if m.focus != focusSidebar {
		t.Error("the refusal put the keyboard in the pane area")
	}
	// Moving on previews the next row as ever.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.tab().focused().view.Row != m.cursor {
		t.Fatalf("the row above was not previewed: %q", m.tab().focused().view.Row)
	}
}

// TestNothingIsTypedIntoOwnPane: however its own pane came to be focused,
// a key or a click sent there would come straight back as input and be
// sent again, for ever. None is sent.
func TestNothingIsTypedIntoOwnPane(t *testing.T) {
	m, peer := a1Fixture(t, true)
	mach := m.machines[0]
	mach.ownPane = "p1"
	m.rebuild()
	seen := len(peer.snapshot())
	m.sendKey(localMachine, "p1", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	m.sendKey(localMachine, "p1", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted"), Paste: true})
	a2Run(m.paneMouse(localMachine, "p1", tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, 1, 1, true, false))
	// Another pane still gets its key — and that is the one the peer sees.
	m.sendKey(localMachine, "p2", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	peer.waitFor(t, "a key for p2", func(msg proto.Message) bool { return strings.Contains(string(msg.Params), `"id":"p2"`) })
	for _, msg := range peer.snapshot()[seen:] {
		if strings.Contains(string(msg.Params), `"id":"p1"`) {
			t.Fatalf("sent to its own pane: %s %s", msg.Method, msg.Params)
		}
	}
}
