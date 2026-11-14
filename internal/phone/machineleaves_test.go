package phone

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
)

// A machine's connection can leave a socket while the phone has its panes
// open: the machine is taken from the device, or its ssh bridge drops.
// The subscriptions end with that connection, so what the socket keeps
// about them has to go too — otherwise the pane reads as drawn when the
// machine is back and opening it again does nothing, and the dead entries
// count toward the limit on panes open at once. Both seen with two real
// servers in review.

// socketClient is an open socket's own connection to machine, for a test
// that has to cut it as a dropped bridge would.
func (f *fixture) socketClient(machine string) *client.Client {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	for s := range f.g.sockets {
		s.mu.Lock()
		c := s.clients[machine]
		s.mu.Unlock()
		if c != nil {
			return c
		}
	}
	return nil
}

// openCount is how many panes the open sockets have open on machine.
func (f *fixture) openCount(machine string) int {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	n := 0
	for s := range f.g.sockets {
		s.mu.Lock()
		for id := range s.open {
			if fleet.MachineOf(id) == machine {
				n++
			}
		}
		for id := range s.sizes {
			if fleet.MachineOf(id) == machine {
				n++
			}
		}
		s.mu.Unlock()
	}
	return n
}

// drawsAgain waits for frame.open of ref to bring a frame showing marker.
// It asks again each time round, as a person tapping the pane would: until
// the socket has its new connection, an open can be refused as the machine
// still being reached.
func drawsAgain(t *testing.T, s *fakeSocket, ref, marker string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.send(ClientMessage{Type: MsgFrameOpen, ID: "again", Pane: ref})
		for _, m := range s.until("drawn?") {
			if m.Type == MsgFrame && m.Frame != nil && m.Frame.Pane == ref &&
				strings.Contains(strings.Join(m.Frame.Lines, "\n"), marker) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never drew again on this socket", ref)
}

func TestAMachineLeavingTheSocketLetsItsPanesDrawAgain(t *testing.T) {
	for _, how := range []string{"revoked", "bridge dropped"} {
		t.Run(how, func(t *testing.T) {
			old := machineJoinEvery
			machineJoinEvery = 50 * time.Millisecond
			t.Cleanup(func() { machineJoinEvery = old })

			f := newFixture(t)
			other := addMachine(t, f, "busybox", true)
			p := f.pair(PermFull)
			f.grant(p, "busybox")
			there := startPaneOn(t, other, "claude", "stty raw -echo; printf 'from-busybox\\r\\n'; exec cat")
			ref := fleet.ComposePaneID("busybox", there)
			waitMachine(t, f, p, "busybox", MachineOnline)

			s := p.socket()
			s.send(ClientMessage{Type: MsgPanesWatch, ID: "w1"})
			s.send(ClientMessage{Type: MsgFrameOpen, ID: "f1", Pane: ref})
			s.next("a frame from busybox", func(m ServerMessage) bool {
				return m.Type == MsgFrame && m.Frame != nil && m.Frame.Pane == ref
			})

			switch how {
			case "revoked":
				f.grant(p)
				waitFor(t, "the socket to let busybox go", func() bool { return !f.socketJoined("busybox") })
				f.grant(p, "busybox")
			case "bridge dropped":
				c := f.socketClient("busybox")
				if c == nil {
					t.Fatal("the socket has no connection to busybox")
				}
				c.Close()
				waitFor(t, "the socket to let busybox go", func() bool { return f.socketClient("busybox") != c })
			}
			drawsAgain(t, s, ref, "from-busybox")
			if n := f.openCount("busybox"); n != 2 { // the pane in open and its size
				t.Errorf("busybox entries after drawing again: %d", n)
			}
		})
	}
}

// TestAMachineLeavingFreesItsPlacesOnTheSocket: eight open on a machine
// that is then taken away must not leave the socket full.
func TestAMachineLeavingFreesItsPlacesOnTheSocket(t *testing.T) {
	old := machineJoinEvery
	machineJoinEvery = 50 * time.Millisecond
	t.Cleanup(func() { machineJoinEvery = old })

	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	s := p.socket()
	for i := range maxOpenFrames {
		ref := fleet.ComposePaneID("busybox", startPaneOn(t, other, "", "exec sleep 30"))
		s.send(ClientMessage{Type: MsgFrameOpen, ID: "o" + string(rune('0'+i)), Pane: ref})
	}
	for _, m := range s.until("opened") {
		if m.Type == MsgError {
			t.Fatalf("opening on busybox: %+v", m.Error)
		}
	}
	f.grant(p)
	waitFor(t, "the socket to let busybox go", func() bool { return !f.socketJoined("busybox") })
	here := f.pane("", "stty raw -echo; printf 'here-marker\\r\\n'; exec cat")
	drawsAgain(t, s, phoneID(here), "here-marker")
	if n := f.openCount("busybox"); n != 0 {
		t.Errorf("%d busybox entries kept", n)
	}
}

// TestFrameCloseForgetsAPaneItCannotReach: between a machine being taken
// away and the socket noticing (a tick of follow, turned off here), the
// phone may close what it had open there. Closing has to free the place
// even though the machine can no longer be asked to unsubscribe — it used
// to be refused as forbidden, and the place stayed taken. A close of
// something never opened on a machine it cannot reach is still refused.
func TestFrameCloseForgetsAPaneItCannotReach(t *testing.T) {
	old := machineJoinEvery
	machineJoinEvery = time.Hour
	t.Cleanup(func() { machineJoinEvery = old })

	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	s := p.socket()
	var refs []string
	for i := range maxOpenFrames {
		ref := fleet.ComposePaneID("busybox", startPaneOn(t, other, "", "exec sleep 30"))
		refs = append(refs, ref)
		s.send(ClientMessage{Type: MsgFrameOpen, ID: "o" + string(rune('0'+i)), Pane: ref})
	}
	for _, m := range s.until("opened") {
		if m.Type == MsgError {
			t.Fatalf("opening on busybox: %+v", m.Error)
		}
	}
	f.grant(p)
	for _, ref := range refs {
		s.send(ClientMessage{Type: MsgFrameClose, ID: "c", Pane: ref})
	}
	for _, m := range s.until("closed") {
		if m.Type == MsgError {
			t.Fatalf("closing a pane open on a machine taken away: %+v", m.Error)
		}
	}
	if n := f.openCount("busybox"); n != 0 {
		t.Fatalf("%d entries kept after closing", n)
	}
	s.send(ClientMessage{Type: MsgFrameClose, ID: "never", Pane: "busybox:p999"})
	got := s.next("the refusal", func(m ServerMessage) bool { return m.Type == MsgError && m.ID == "never" })
	if got.Error.Code != CodeForbidden {
		t.Fatalf("closing what was never open there: %+v", got.Error)
	}
	s.send(ClientMessage{Type: MsgFrameClose, ID: "bad", Pane: "not a pane"})
	if got := s.next("bad id", func(m ServerMessage) bool { return m.Type == MsgError && m.ID == "bad" }); got.Error.Code != CodeBadRequest {
		t.Fatalf("a bad id: %+v", got.Error)
	}

	here := f.pane("", "stty raw -echo; printf 'here-marker\\r\\n'; exec cat")
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "local", Pane: phoneID(here)})
	s.next("a frame from here", func(m ServerMessage) bool {
		if m.Type == MsgError && m.ID == "local" {
			t.Fatalf("a pane here was refused: %+v", m.Error)
		}
		return m.Type == MsgFrame && m.Frame != nil && m.Frame.Pane == phoneID(here)
	})
}
