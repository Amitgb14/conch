package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// heldTyped lists what a peer received as typing into pane id: text as
// is, named keys in <>.
func heldTyped(peer *a1Peer, id string) []string {
	var out []string
	for _, msg := range peer.snapshot() {
		switch msg.Method {
		case proto.MethodPaneSendText:
			var p proto.PaneSendTextParams
			if json.Unmarshal(msg.Params, &p) == nil && p.ID == id {
				out = append(out, p.Text)
			}
		case proto.MethodPaneSendKeys:
			var p proto.PaneSendKeysParams
			if json.Unmarshal(msg.Params, &p) == nil && p.ID == id {
				out = append(out, "<"+strings.Join(p.Keys, " ")+">")
			}
		}
	}
	return out
}

// heldWait waits until pane id has received want (and no more).
func heldWait(t *testing.T, peer *a1Peer, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := strings.Join(heldTyped(peer, id), "|")
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s received %q, want %q", id, got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func heldUpdate(t *testing.T, m *Model, msg tea.Msg) tea.Cmd {
	t.Helper()
	next, cmd := m.Update(msg)
	*m = next.(Model)
	return cmd
}

// heldTyping opens the bash pane p3 with focus in it, as typing into it.
func heldTyping(t *testing.T) (*Model, *machine) {
	t.Helper()
	m, _ := a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	m.focus = focusMain
	m.syncView()
	return m, m.machines[0]
}

// heldReconnect connects machine mach again and lists panes, as after a
// server reload, returning the new server's peer.
func heldReconnect(t *testing.T, m *Model, mach *machine, panes []proto.PaneInfo) *a1Peer {
	t.Helper()
	c, peer := a1FakeClient(t)
	heldUpdate(t, m, machineConnectedMsg{machine: mach.id, gen: mach.gen, c: c})
	heldUpdate(t, m, panesMsg{machine: mach.id, gen: mach.gen, panes: panes})
	return peer
}

// Keys typed while the server reloads used to move focus to the tree and
// run as tree commands; they reach the pane once it is back.
func TestHeldTypingAcrossReload(t *testing.T) {
	m, mach := heldTyping(t)
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	if m.focus != focusMain {
		t.Fatal("a dropped connection took focus from the pane")
	}
	for _, k := range []tea.KeyMsg{runes("n"), runes("x"), {Type: tea.KeyEnter}} {
		a1Key(t, m, k)
	}
	if m.overlay != nil || m.focus != focusMain || len(mach.panes) != 4 {
		t.Fatalf("typing ran tree commands: overlay %T focus %v", m.overlay, m.focus)
	}
	if !strings.Contains(m.flash, "typing is held") {
		t.Fatalf("flash %q", m.flash)
	}

	// Typed after connecting but before the panes are listed: still held,
	// so it follows the earlier keys.
	c, peer := a1FakeClient(t)
	heldUpdate(t, m, machineConnectedMsg{machine: localMachine, gen: mach.gen, c: c})
	a1Key(t, m, runes("y"))
	time.Sleep(50 * time.Millisecond)
	if got := heldTyped(peer, "p3"); len(got) != 0 {
		t.Fatalf("sent before the panes were listed: %q", got)
	}
	heldUpdate(t, m, panesMsg{machine: localMachine, gen: mach.gen, panes: mach.panes})
	heldWait(t, peer, "p3", "n|x|<enter>|y")

	// Afterwards typing goes straight through.
	a1Key(t, m, runes("z"))
	heldWait(t, peer, "p3", "n|x|<enter>|y|z")
	if len(mach.held) != 0 {
		t.Fatalf("held %d", len(mach.held))
	}
}

func TestHeldRetriesSoonAfterLoss(t *testing.T) {
	m, mach := heldTyping(t)
	start := time.Now()
	cmd := heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	var retry bool
	for _, msg := range a2Run(cmd) {
		if r, ok := msg.(machineRetryMsg); ok && r.gen == mach.gen {
			retry = true
		}
	}
	if !retry || time.Since(start) > time.Second {
		t.Fatalf("retry %v after %v", retry, time.Since(start))
	}
}

func TestHeldDroppedForOtherPanes(t *testing.T) {
	m, mach := heldTyping(t)
	created := time.Now().Add(-time.Hour)
	for i := range mach.panes {
		mach.panes[i].Created = created
	}
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	a1Key(t, m, runes("a"))

	// A restarted server reusing the pane ID: a different pane, keys dropped.
	panes := append([]proto.PaneInfo(nil), mach.panes...)
	panes[2].Created = time.Now()
	peer := heldReconnect(t, m, mach, panes)
	if !m.flashIsErr || !strings.Contains(m.flash, "dropped 1 keys") {
		t.Fatalf("flash %q", m.flash)
	}

	// An exited pane, or one gone, gets nothing either.
	for _, change := range []func([]proto.PaneInfo) []proto.PaneInfo{
		func(ps []proto.PaneInfo) []proto.PaneInfo { ps[2].State = proto.PaneExited; return ps },
		func(ps []proto.PaneInfo) []proto.PaneInfo { return append(ps[:2], ps[3:]...) },
	} {
		heldUpdate(t, m, panesMsg{machine: localMachine, gen: mach.gen, panes: panes})
		heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
		mach.held = append(mach.held, heldKey{pane: "p3", created: panes[2].Created, key: runes("b"), at: time.Now()})
		m.setFlash("", false)
		c, p2 := a1FakeClient(t)
		heldUpdate(t, m, machineConnectedMsg{machine: localMachine, gen: mach.gen, c: c})
		heldUpdate(t, m, panesMsg{machine: localMachine, gen: mach.gen, panes: change(append([]proto.PaneInfo(nil), panes...))})
		if len(mach.held) != 0 || !strings.Contains(m.flash, "dropped 1 keys") {
			t.Fatalf("held %d flash %q", len(mach.held), m.flash)
		}
		time.Sleep(20 * time.Millisecond)
		if got := heldTyped(p2, "p3"); len(got) != 0 {
			t.Fatalf("sent %q", got)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if got := heldTyped(peer, "p3"); len(got) != 0 {
		t.Fatalf("restarted pane got %q", got)
	}
}

func TestHeldTooOldDropped(t *testing.T) {
	m, mach := heldTyping(t)
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	a1Key(t, m, runes("o"))
	a1Key(t, m, runes("k"))
	mach.held[0].at = time.Now().Add(-heldKeyAge - time.Second)
	peer := heldReconnect(t, m, mach, mach.panes)
	heldWait(t, peer, "p3", "k")
	if !strings.Contains(m.flash, "dropped 1 keys") {
		t.Fatalf("flash %q", m.flash)
	}
}

// A pane list that never arrives mustn't hold typing forever.
func TestHeldWithoutPaneList(t *testing.T) {
	m, mach := heldTyping(t)
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	a1Key(t, m, runes("a"))
	c, peer := a1FakeClient(t)
	heldUpdate(t, m, machineConnectedMsg{machine: localMachine, gen: mach.gen, c: c})
	a1Key(t, m, runes("b"))
	if len(mach.held) != 2 {
		t.Fatalf("held %d", len(mach.held))
	}
	for i := range mach.held {
		mach.held[i].at = time.Now().Add(-heldKeyAge - time.Second)
	}
	a1Key(t, m, runes("c"))
	heldWait(t, peer, "p3", "c")
	if len(mach.held) != 0 {
		t.Fatalf("held %d", len(mach.held))
	}
}

func TestHeldLimit(t *testing.T) {
	m, mach := heldTyping(t)
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	for range maxHeldKeys + 5 {
		a1Key(t, m, runes("a"))
	}
	if len(mach.held) != maxHeldKeys || !m.flashIsErr || !strings.Contains(m.flash, "typing dropped") {
		t.Fatalf("held %d flash %q", len(mach.held), m.flash)
	}
}

func TestHeldPrefixAndSyncedSplits(t *testing.T) {
	m, mach := heldTyping(t)
	// The prefix twice types the prefix key itself.
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if len(mach.held) != 1 || mach.held[0].pane != "p3" {
		t.Fatalf("held %+v", mach.held)
	}
	peer := heldReconnect(t, m, mach, mach.panes)
	heldWait(t, peer, "p3", "<ctrl+b>")

	// A synchronized tab holds the key for every split.
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.tab().sync = true
	m.focus = focusMain
	m.syncView()
	focused := m.tab().focused().view.PaneID
	other := map[string]string{"p2": "p3", "p3": "p2"}[focused]
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	a1Key(t, m, runes("s"))
	peer = heldReconnect(t, m, mach, mach.panes)
	heldWait(t, peer, focused, "s")
	heldWait(t, peer, other, "s")
}

// A machine that stays offline keeps its last panes; typing into a pane on
// a machine the TUI no longer knows does nothing.
func TestHeldUnknownMachineOrPane(t *testing.T) {
	m, mach := heldTyping(t)
	m.sendKey("gone", "p3", runes("a"))
	heldUpdate(t, m, machineClosedMsg{machine: localMachine, gen: mach.gen})
	m.sendKey(localMachine, "nope", runes("a"))
	if len(mach.held) != 0 {
		t.Fatalf("held %+v", mach.held)
	}
}
