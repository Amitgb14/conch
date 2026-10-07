package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestWheelOverAnotherSplitAsksThatPane: reported from a real session, with
// the screenshot. Scrolling over one split printed `<65;106;43M` into an
// agent's prompt — an SGR mouse report arriving as text, because conch
// decided what to do with the wheel from the *focused* pane's frame while
// acting on the pane under the pointer. The focused one took the mouse; the
// one scrolled over had never asked for it.
func TestWheelOverAnotherSplitAsksThatPane(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	c, peer := a1FakeClient(t, "pane.scroll.v1")
	m.machines[0].c = c
	m.viewing, m.viewMachine = "p1", localMachine

	// p1 is focused and takes the mouse; p2 is another split and does not.
	a2Frame(m, "p1", &proto.Frame{Mouse: true, History: 100, Lines: make([]string, 10)})
	a2Frame(m, "p2", &proto.Frame{Mouse: false, History: 0, Lines: make([]string, 10)})

	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	a2Run(m.paneMouse(localMachine, "p2", wheel, 1, 1, false, true))
	// Nothing was sent for p2 — proved by sending for p1 afterwards and
	// waiting for *that*: one connection keeps its order, so p2's would
	// have to be there first. Looking at the queue straight after the
	// wheel would prove nothing, the notify being on its way.
	//
	// p1 is on the main screen, where the wheel is conch's history, so it
	// takes alt to send one at all (TestTheWheelOnTheMainScreenIsConchs).
	held := wheel
	held.Alt = true
	a2Run(m.paneMouse(localMachine, "p1", held, 1, 1, false, true))
	peer.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodPaneSendMouse && strings.Contains(string(msg.Params), `"id":"p2"`) {
			t.Fatalf("a pane that never asked for the mouse was sent one: %s", msg.Params)
		}
	}
	// Nor did the wheel over p2 scroll the focused pane, which would move
	// the wrong pane under the pointer. (p1's own wheel above was held
	// with alt, which goes to the program rather than to the history.)
	if m.offset != 0 {
		t.Fatalf("it scrolled the focused pane: offset %d", m.offset)
	}

	// And once that split is the focused one, its wheel scrolls conch's
	// history for it — the pane under the pointer deciding, not p1's.
	m.viewing = "p2"
	m.frame = m.frames[paneKey(localMachine, "p2")]
	a2Frame(m, "p2", &proto.Frame{Mouse: false, History: 50, Lines: make([]string, 10)})
	m.frame = m.frames[paneKey(localMachine, "p2")]
	a2Run(m.paneMouse(localMachine, "p2", wheel, 1, 1, false, true))
	if m.offset == 0 {
		t.Fatal("the focused split's own wheel did not scroll its history")
	}
}

// TestMouseGoesToThePaneUnderThePointersMachine: the same mistake across
// machines — the focused machine's client with another machine's pane id
// would have typed into whatever pane happened to carry that id there.
func TestMouseGoesToThePaneUnderThePointersMachine(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.viewing, m.viewMachine = "p1", localMachine
	local, localPeer := a1FakeClient(t, "pane.scroll.v1")
	m.machines[0].c = local

	// A second machine, with a pane that does take the mouse.
	other := newMachine("busybox", "busybox", "busybox.example")
	other.state = stateOnline
	remote, remotePeer := a1FakeClient(t, "pane.scroll.v1")
	other.c = remote
	m.machines = append(m.machines, other)
	a2Frame(m, "p1", &proto.Frame{Mouse: true, Lines: make([]string, 10)})
	m.frames[paneKey("busybox", "p1")] = &proto.Frame{ID: "p1", Mouse: true, Lines: make([]string, 10)}

	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	a2Run(m.paneMouse("busybox", "p1", wheel, 1, 1, false, true))
	remotePeer.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
	for _, msg := range localPeer.snapshot() {
		if msg.Method == proto.MethodPaneSendMouse {
			t.Fatalf("it went to this computer instead: %s", msg.Params)
		}
	}
}

// TestTheWheelOnTheMainScreenIsConchs: an agent asks for mouse reporting
// to take clicks and does nothing with the wheel, so forwarding it there
// printed the reports into the agent's prompt as `<65;121;38M` — reported
// twice from real use, and the pane genuinely held the mouse both times
// (`mouse=True altScreen=False history=5000` off the live server), which
// is why sending it to the right pane had not cured it.
//
// So the rule is the one tmux has: on the main screen the wheel scrolls
// conch's own history, because that is what conch is keeping it for and
// what the person meant. On the alternate screen it is the program's, as
// there is no history of conch's there and the program has its own.
// Alt or ctrl hands it over either way, as for a link in a pane.
func TestTheWheelOnTheMainScreenIsConchs(t *testing.T) {
	a2Isolate(t)
	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}

	// An agent on the main screen, holding the mouse, with history: ours.
	m := a2Model()
	c, peer := a1FakeClient(t, "pane.scroll.v1")
	m.machines[0].c = c
	m.viewing, m.viewMachine = "p1", localMachine
	a2Frame(m, "p1", &proto.Frame{Mouse: true, History: 5000, Lines: make([]string, 10)})
	m.frame = m.frames[paneKey(localMachine, "p1")]
	a2Run(m.paneMouse(localMachine, "p1", wheel, 1, 1, false, true))
	if m.offset == 0 {
		t.Fatal("the wheel did not scroll conch's history")
	}
	// Held with alt, the program gets it: a program that really wants the
	// wheel is not shut out, it is just not the default.
	held := wheel
	held.Alt = true
	a2Run(m.paneMouse(localMachine, "p1", held, 1, 1, false, true))
	peer.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
	// And the plain wheel sent nothing at all: alt's report would be
	// behind it on one connection if it had.
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodPaneSendMouse && !strings.Contains(string(msg.Params), `"modifiers"`) {
			// the alt one carries its modifier; anything else is the leak
			if !strings.Contains(string(msg.Params), "alt") {
				t.Fatalf("a plain wheel was sent to a program on the main screen: %s", msg.Params)
			}
		}
	}

	// The same agent on the alternate screen, where conch keeps nothing:
	// the wheel is the program's, or its own scrollback is unreachable.
	m2 := a2Model()
	c2, peer2 := a1FakeClient(t, "pane.scroll.v1")
	m2.machines[0].c = c2
	m2.viewing, m2.viewMachine = "p1", localMachine
	a2Frame(m2, "p1", &proto.Frame{Mouse: true, AltScreen: true, History: 0, Lines: make([]string, 10)})
	m2.frame = m2.frames[paneKey(localMachine, "p1")]
	a2Run(m2.paneMouse(localMachine, "p1", wheel, 1, 1, false, true))
	peer2.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
	if m2.offset != 0 {
		t.Errorf("it scrolled a pane with no history of conch's: offset %d", m2.offset)
	}

	// A main-screen program holding the mouse with nothing kept for it —
	// a pane whose history conch has dropped — still gets the wheel,
	// since scrolling nothing would simply swallow it.
	m3 := a2Model()
	c3, peer3 := a1FakeClient(t, "pane.scroll.v1")
	m3.machines[0].c = c3
	m3.viewing, m3.viewMachine = "p1", localMachine
	a2Frame(m3, "p1", &proto.Frame{Mouse: true, History: 0, Lines: make([]string, 10)})
	m3.frame = m3.frames[paneKey(localMachine, "p1")]
	a2Run(m3.paneMouse(localMachine, "p1", wheel, 1, 1, false, true))
	peer3.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
}
