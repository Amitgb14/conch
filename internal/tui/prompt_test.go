package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4Model is the fixture with a server that knows agent.prompt.
func a4Model(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	var c *client.Client
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.rebuild()
	return m, peer
}

func a4MenuLabels(m *Model, id string) []string {
	i := indexOfRow(m.rows, id)
	if i < 0 {
		return nil
	}
	mu := newRowMenu(*m, m.rows[i], 0, 0)
	var out []string
	for _, it := range mu.items {
		out = append(out, ansi.Strip(it.label))
	}
	return out
}

func TestPromptFromThePaneMenu(t *testing.T) {
	m, peer := a4Model(t, proto.CapAgentPrompt)
	id := paneNodeID(localMachine, "p1")
	a1At(t, m, id)

	labels := strings.Join(a4MenuLabels(m, id), " | ")
	if !strings.Contains(labels, "Prompt…") {
		t.Fatalf("the pane menu has no way to prompt:\n%s", labels)
	}
	// A terminal has no agent, so it is not offered one.
	if l := strings.Join(a4MenuLabels(m, paneNodeID(localMachine, "p2")), " | "); strings.Contains(l, "Prompt") {
		t.Fatalf("a terminal was offered a prompt:\n%s", l)
	}

	// Asking opens a dialog; an empty message is refused.
	if cmd := m.openPrompt(localMachine, "p1"); cmd == nil {
		t.Fatal("no dialog")
	}
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("overlay %T", m.overlay)
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"   "}))); msg != "nothing to send" {
		t.Fatalf("empty message: %q", msg)
	}

	// Sending remembers the turn that answers it.
	peer.setResult(proto.MethodAgentPrompt, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 4})
	a2Run(d.submit(m, []string{"review the diff"}))
	sent := a2Run(m.sendPrompt(localMachine, "p1", "review the diff"))
	if len(sent) != 1 {
		t.Fatalf("send gave %d messages", len(sent))
	}
	msg, ok := sent[0].(promptSentMsg)
	if !ok {
		t.Fatalf("send gave %T", sent[0])
	}
	m.receivePromptSent(msg)
	w := m.prompts[promptKey(localMachine, "p1")]
	if w == nil || w.turn != 4 || w.agent != "claude" {
		t.Fatalf("watch %+v", w)
	}
	if !strings.Contains(m.flash, "you'll be told when that work ends") {
		t.Fatalf("flash %q", m.flash)
	}

	// An agent asking a question of its own is refused by the server, and
	// its words are what the person reads.
	peer.setCodedError(proto.MethodAgentPrompt, "agent_blocked", "claude in p1 is waiting for an answer")
	sent = a2Run(m.sendPrompt(localMachine, "p1", "again"))
	m.receivePromptSent(sent[0].(promptSentMsg))
	if !strings.Contains(m.flash, "waiting for an answer") {
		t.Fatalf("a blocked agent: %q", m.flash)
	}
}

func TestPromptNeedsTheCapability(t *testing.T) {
	m, _ := a4Model(t) // a server that knows nothing
	id := paneNodeID(localMachine, "p1")
	a1At(t, m, id)
	labels := strings.Join(a4MenuLabels(m, id), " | ")
	if !strings.Contains(labels, "its server is too old") {
		t.Fatalf("an old server is not explained:\n%s", labels)
	}
	if cmd := m.openPrompt(localMachine, "p1"); cmd != nil || !strings.Contains(m.flash, "predates prompting") {
		t.Fatalf("it asked anyway: %q", m.flash)
	}
	if len(m.prompts) != 0 {
		t.Fatalf("watches: %v", m.prompts)
	}
}

func TestPromptWatchReportsTheEnd(t *testing.T) {
	m, _ := a4Model(t, proto.CapAgentPrompt)
	mach := m.machines[0]
	pane := func(state string, turn int) proto.PaneInfo {
		return proto.PaneInfo{ID: "p1", Name: "claude", State: proto.PaneRunning,
			Agent: &proto.AgentStatus{Name: "claude", State: state, Turn: turn}}
	}
	arm := func() { m.prompts = map[string]*promptWatch{promptKey(localMachine, "p1"): {agent: "claude", turn: 4}} }

	// Nothing was asked: nothing is said.
	m.prompts = nil
	if cmd := m.watchPrompt(mach, pane(proto.AgentDone, 9), false); cmd != nil {
		t.Fatal("reported work nobody asked for")
	}
	// Still working, and a turn older than the message, are both silence.
	arm()
	m.watchPrompt(mach, pane(proto.AgentWorking, 4), false)
	m.watchPrompt(mach, pane(proto.AgentDone, 3), false)
	if m.prompts[promptKey(localMachine, "p1")] == nil {
		t.Fatal("the watch was dropped before the work ended")
	}
	// Done, with that turn behind it.
	m.watchPrompt(mach, pane(proto.AgentDone, 4), false)
	if !strings.Contains(m.flash, "finished what you asked") || m.prompts[promptKey(localMachine, "p1")] != nil {
		t.Fatalf("done: %q, watch %v", m.flash, m.prompts)
	}
	// A question ends it whatever the turn: none was open when it went in.
	arm()
	m.watchPrompt(mach, pane(proto.AgentBlocked, 1), false)
	if !strings.Contains(m.flash, "is waiting for you") {
		t.Fatalf("blocked: %q", m.flash)
	}
	// The pane ends, or another agent takes it over.
	arm()
	m.watchPrompt(mach, pane(proto.AgentDone, 4), true)
	if !strings.Contains(m.flash, "ended before it answered") {
		t.Fatalf("exited: %q", m.flash)
	}
	arm()
	other := pane(proto.AgentDone, 9)
	other.Agent.Name = "codex"
	m.watchPrompt(mach, other, false)
	if !strings.Contains(m.flash, "is not claude any more") {
		t.Fatalf("another agent: %q", m.flash)
	}
	// Told once: a second update says nothing again.
	m.flash = ""
	m.watchPrompt(mach, pane(proto.AgentDone, 4), false)
	if m.flash != "" {
		t.Fatalf("said twice: %q", m.flash)
	}
}
