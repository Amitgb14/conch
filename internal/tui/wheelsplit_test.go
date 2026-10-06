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
	a2Run(m.paneMouse(localMachine, "p1", wheel, 1, 1, false, true))
	peer.waitMethod(t, proto.MethodPaneSendMouse, `"id":"p1"`)
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodPaneSendMouse && strings.Contains(string(msg.Params), `"id":"p2"`) {
			t.Fatalf("a pane that never asked for the mouse was sent one: %s", msg.Params)
		}
	}
	// Nor was the focused pane scrolled instead, which would move the
	// wrong pane under the pointer.
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
