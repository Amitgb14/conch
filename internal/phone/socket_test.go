package phone

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/Amitgb14/conch/internal/proto"
)

// The socket's whole round: the list, an agent appearing and changing, a
// pane drawn and typed into, and the agent leaving.
func TestSocket(t *testing.T) {
	f := newFixture(t)
	s := f.pair(PermFull).socket()

	// Nothing is pushed before the phone asks to watch.
	early := f.pane("claude", "stty -echo; exec cat")
	if msgs := s.until("quiet"); len(msgs) != 0 {
		t.Fatalf("sent before agents.watch: %+v", msgs)
	}

	s.send(ClientMessage{Type: MsgAgentsWatch, ID: "w1"})
	list := s.next("the list", func(m ServerMessage) bool { return m.Type == MsgAgents })
	if list.ID != "w1" || list.Agents == nil || len(*list.Agents) != 1 || (*list.Agents)[0].Pane != early {
		t.Fatalf("agents %+v", list)
	}

	// An agent that starts later arrives by itself, and so does its question.
	pane := f.pane("claude", "stty raw -echo; printf 'booted-marker\\r\\n'; exec cat")
	s.next("the new agent", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent.Pane == pane && m.Agent.Agent == "claude" && m.Agent.State == StateIdle
	})
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: pane, Agent: "claude", Event: "PermissionRequest", Message: "May I?"}, nil)
	asked := s.next("its question", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent.Pane == pane && m.Agent.State == StateWaiting
	})
	if q := asked.Agent.Question; q == nil || q.ID == "" || q.Choices == nil {
		t.Fatalf("question %+v", asked.Agent.Question)
	}

	// Frames come only for a pane that was opened.
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "f1", Pane: pane})
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "f1", Pane: pane}) // twice is once
	frame := s.next("a frame", func(m ServerMessage) bool {
		return m.Type == MsgFrame && strings.Contains(strings.Join(m.Frame.Lines, "\n"), "booted-marker")
	})
	if frame.Frame.Pane != pane || frame.Frame.Cols != 100 || frame.Frame.Rows != 24 || len(frame.Frame.Lines) != 24 {
		t.Fatalf("frame %+v", frame.Frame)
	}
	s.send(ClientMessage{Type: MsgKeys, ID: "k1", Pane: pane, Keys: []string{"Q", "W"}})
	s.next("the keys drawn", func(m ServerMessage) bool {
		return m.Type == MsgFrame && m.Frame.Pane == pane && strings.Contains(strings.Join(m.Frame.Lines, "\n"), "QW")
	})

	// What the contract refuses, each with the message's own id.
	for _, c := range []struct {
		m    ClientMessage
		code string
	}{
		{ClientMessage{Type: "shell.exec", ID: "e1"}, CodeBadRequest},
		{ClientMessage{ID: "e2"}, CodeBadRequest},
		{ClientMessage{Type: MsgFrameOpen, ID: "e3", Pane: "reviewer"}, CodeBadRequest},
		{ClientMessage{Type: MsgFrameOpen, ID: "e4", Pane: "p999"}, CodeNotFound},
		{ClientMessage{Type: MsgFrameClose, ID: "e5", Pane: ""}, CodeBadRequest},
		{ClientMessage{Type: MsgKeys, ID: "e6", Pane: pane}, CodeBadRequest},
		{ClientMessage{Type: MsgKeys, ID: "e7", Pane: pane, Keys: make([]string, maxKeys+1)}, CodeBadRequest},
		{ClientMessage{Type: MsgKeys, ID: "e8", Pane: pane, Keys: []string{"hyper+x"}}, CodeBadRequest},
		{ClientMessage{Type: MsgKeys, ID: "e9", Pane: "p999", Keys: []string{"x"}}, CodeNotFound},
	} {
		s.send(c.m)
		got := s.next("the refusal of "+c.m.ID, func(m ServerMessage) bool { return m.Type == MsgError && m.ID == c.m.ID })
		if got.Error == nil || got.Error.Code != c.code || got.Error.Message == "" {
			t.Errorf("%s: %+v, want %s", c.m.ID, got.Error, c.code)
		}
	}
	// Not JSON, and not text: refused, and the socket carries on.
	ctx := context.Background()
	s.conn.Write(ctx, websocket.MessageText, []byte("{"))
	s.next("the refusal of bad JSON", func(m ServerMessage) bool { return m.Type == MsgError && m.Error.Code == CodeBadRequest })
	s.conn.Write(ctx, websocket.MessageBinary, []byte(`{"type":"ping"}`))
	s.next("the refusal of a binary message", func(m ServerMessage) bool { return m.Type == MsgError && m.Error.Code == CodeBadRequest })

	// A closed pane's frames stop; the keys typed above are still there
	// to draw, so a frame would come if it were open.
	s.send(ClientMessage{Type: MsgFrameClose, Pane: pane})
	s.until("closed")
	f.call(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: []string{"J"}}, nil)
	waitFor(t, "the key on screen", func() bool { return strings.Contains(f.screen(pane), "QWJ") })
	for _, m := range s.until("after-close") {
		if m.Type == MsgFrame {
			t.Fatalf("a frame after frame.close: %+v", m.Frame)
		}
	}

	// No more than a few panes drawn at once.
	var opened []string
	for range maxOpenFrames {
		id := f.pane("", "exec sleep 30")
		opened = append(opened, id)
		s.send(ClientMessage{Type: MsgFrameOpen, Pane: id})
	}
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "many", Pane: pane})
	got := s.next("the limit", func(m ServerMessage) bool { return m.Type == MsgError && m.ID == "many" })
	if got.Error.Code != CodeBadRequest {
		t.Fatalf("over the limit: %+v", got.Error)
	}

	// The agent's pane closes: gone. A shell's closing is nobody's news.
	f.call(proto.MethodPaneClose, proto.PaneRef{ID: opened[0]}, nil)
	f.call(proto.MethodPaneClose, proto.PaneRef{ID: pane}, nil)
	gone := s.next("agent.gone", func(m ServerMessage) bool { return m.Type == MsgAgentGone })
	if gone.Pane != pane {
		t.Fatalf("gone %+v", gone)
	}
	// Once, though the pane both exits and closes.
	for _, m := range s.until("after-gone") {
		if m.Type == MsgAgentGone {
			t.Fatalf("a second agent.gone: %+v", m)
		}
	}
}

// Revoking a device closes the sockets it has open, at once — the file is
// changed as `conch web revoke` changes it, from outside the gateway — and
// leaves other devices' alone.
func TestRevokeClosesOpenSockets(t *testing.T) {
	f := newFixture(t)
	p, other := f.pair(PermFull), f.pair(PermFull)
	pane := f.pane("", "stty raw -echo; printf 'ready>'; exec cat")
	s1, s2, kept := p.socket(), p.socket(), other.socket()
	for _, s := range []*fakeSocket{s1, s2, kept} {
		s.until("open")
	}

	if ok, err := OpenStore(f.dir).Revoke(p.id); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	for _, s := range []*fakeSocket{s1, s2} {
		msgs := s.closed()
		if len(msgs) != 1 || msgs[0].Type != MsgBye || msgs[0].Reason != ByeRevoked {
			t.Fatalf("a revoked device's socket ended with %+v", msgs)
		}
	}
	if msgs := kept.until("still here"); len(msgs) != 0 {
		t.Fatalf("another device's socket: %+v", msgs)
	}
	if status, _ := p.do("GET", "/api/hello", nil); status != 401 {
		t.Fatalf("a revoked token: %d", status)
	}
	if conn, _, err := dialSocket(f.web.URL, p.token); err == nil {
		conn.CloseNow()
		t.Fatal("a revoked token opened a socket")
	}
	if got := f.screen(pane); strings.Contains(got, "Z") {
		t.Fatalf("typed: %q", got)
	}
	if ok, _ := f.store.Revoke(p.id); ok {
		t.Fatal("revoked twice")
	}
}

// A message from a device revoked a moment ago — before the gateway has
// looked — does nothing: the check is made on the message itself.
func TestRevokedDeviceMessageDoesNothing(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	pane := f.pane("", "stty raw -echo; printf 'ready>'; exec cat")
	waitFor(t, "the pane's prompt", func() bool { return strings.Contains(f.screen(pane), "ready>") })
	s := p.socket()
	s.until("open")

	// Hold the watcher off, so only the message's own check can refuse.
	f.g.mu.Lock()
	if ok, err := f.store.Revoke(p.id); !ok || err != nil {
		f.g.mu.Unlock()
		t.Fatalf("revoke: %v %v", ok, err)
	}
	s.send(ClientMessage{Type: MsgKeys, Pane: pane, Keys: []string{"Z"}})
	f.g.mu.Unlock()

	msgs := s.closed()
	if len(msgs) != 1 || msgs[0].Reason != ByeRevoked {
		t.Fatalf("ended with %+v", msgs)
	}
	// A key of the test's own lands where the phone's would have.
	f.call(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: []string{"K"}}, nil)
	waitFor(t, "the test's key", func() bool { return strings.Contains(f.screen(pane), "K") })
	if got := f.screen(pane); strings.Contains(got, "Z") {
		t.Fatalf("a revoked device typed: %q", got)
	}
}

func TestSocketsEndWithTheGatewayAndTheServer(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	s := p.socket()
	s.until("open")
	f.g.Close()
	if msgs := s.closed(); len(msgs) != 1 || msgs[0].Type != MsgBye || msgs[0].Reason != ByeShuttingDown {
		t.Fatalf("at shutdown: %+v", msgs)
	}
	if conn, _, err := dialSocket(f.web.URL, p.token); err == nil {
		// Opened as the gateway went: it is told so and closed.
		conn.CloseNow()
	}

	// The conch server stopping ends a socket with server_gone.
	f2 := newFixture(t)
	s2 := f2.pair(PermView).socket()
	s2.until("open")
	f2.call(proto.MethodServerStop, nil, nil)
	if msgs := s2.closed(); len(msgs) != 1 || msgs[0].Type != MsgBye || msgs[0].Reason != ByeServerGone {
		t.Fatalf("server gone: %+v", msgs)
	}
}
