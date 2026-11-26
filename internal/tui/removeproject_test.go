package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

func removeFixture(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	a2Isolate(t)
	m, _ := a1Fixture(t, false)
	var c *client.Client
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c, m.machines[0].server = c, c.Server
	return m, peer
}

// x on a project with panes running in it asks whether they stay or
// close with it, saying how many; closing is asked of the server.
func TestRemoveProjectAsksAboutItsPanes(t *testing.T) {
	m, peer := removeFixture(t, proto.CapProjectRemoveClose)
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("x"))
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("x opened %T", m.overlay)
	}
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, ansi.Strip(it.label))
	}
	got := strings.Join(labels, "\n")
	for _, want := range []string{"Remove; its 1 agent and 1 terminal keep running", "Remove and close its 1 agent and 1 terminal", "Cancel"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in\n%s", want, got)
		}
	}
	// Close, clicked.
	b := mu.render(*m)
	cmd := a1Mouse(t, m, b.x+3, b.y+2, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil {
		t.Fatal("the click asked nothing")
	}
	cmd()
	if p := peer.waitMethod(t, proto.MethodProjectRemove, `"id":"r1"`); !strings.Contains(string(p.Params), `"close":true`) {
		t.Fatalf("asked %s", p.Params)
	}

	// Keep them, by its key: nothing is closed.
	peer2 := peer
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("x"))
	cmd = a1Key(t, m, runes("r"))
	if cmd == nil {
		t.Fatal("r asked nothing")
	}
	cmd()
	n := 0
	for _, msg := range peer2.snapshot() {
		if msg.Method == proto.MethodProjectRemove && !strings.Contains(string(msg.Params), "close") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d plain removes", n)
	}
	// esc asks nothing.
	a1Key(t, m, runes("x"))
	if cmd := a1Key(t, m, a2Key("esc")); cmd != nil || m.overlay != nil {
		t.Fatal("esc")
	}
}

// An older server cannot close them: the plain question, saying they
// keep running. A project with nothing running: the plain question.
func TestRemoveProjectOnAnOlderServerOrEmpty(t *testing.T) {
	m, peer := removeFixture(t)
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("x"))
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(strings.Join(d.text, " "), "its 1 agent and 1 terminal keep running") {
		t.Fatalf("older server: %#v", m.overlay)
	}
	m.overlay = nil
	for i := range m.machines[0].panes {
		m.machines[0].panes[i].State = proto.PaneExited
	}
	a1Key(t, m, runes("x"))
	d, ok = m.overlay.(*dialog)
	if !ok || strings.Contains(strings.Join(d.text, " "), "keep running") {
		t.Fatalf("nothing running: %#v", m.overlay)
	}
	for _, meth := range peer.methods() {
		if meth == proto.MethodProjectRemove {
			t.Fatal("removed without an answer")
		}
	}
}

func TestPanesPhrase(t *testing.T) {
	for _, c := range []struct {
		a, o int
		want string
	}{{1, 0, "1 agent"}, {3, 0, "3 agents"}, {0, 2, "2 terminals"}, {2, 1, "2 agents and 1 terminal"}} {
		if got := panesPhrase(c.a, c.o); got != c.want {
			t.Errorf("%d,%d: %q", c.a, c.o, got)
		}
	}
}
