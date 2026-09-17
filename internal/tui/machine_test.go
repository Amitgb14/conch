package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

func TestA2MachineStateStrings(t *testing.T) {
	for s, want := range map[machineState]string{stateConnecting: "connecting", stateOnline: "online", stateOffline: "offline", stateAttention: "needs attention"} {
		if s.String() != want {
			t.Errorf("%d: %q", s, s.String())
		}
	}
}

func TestA2MachineLifecycle(t *testing.T) {
	mach := newMachine("box", "devbox", "dev@box")
	if mach.agents == nil || mach.sizes == nil || mach.installers == nil || mach.state != stateConnecting {
		t.Fatalf("new machine: %+v", mach)
	}
	if mach.waitEvent() != nil || mach.checkAgents() != nil {
		t.Fatal("nothing to wait on or ask without a client")
	}

	// A connection attempt bumps the generation; the command dials, so it isn't run.
	if cmd := mach.connect(false); cmd == nil || mach.gen != 1 || mach.state != stateConnecting {
		t.Fatalf("connect: gen %d state %v", mach.gen, mach.state)
	}

	// Failures: a plain error retries later, an install problem waits for the user.
	if cmd := mach.connected(machineConnectedMsg{machine: "box", gen: 1, err: errors.New("no route")}); cmd == nil {
		t.Fatal("a failed attempt schedules a retry")
	}
	if mach.state != stateOffline || mach.err != "no route" || mach.failures != 1 {
		t.Fatalf("after failure: %v %q %d", mach.state, mach.err, mach.failures)
	}
	if cmd := mach.connected(machineConnectedMsg{err: &remote.InstallError{Reason: "conch isn't installed there"}}); cmd != nil {
		t.Fatal("installing is the user's call")
	}
	if mach.state != stateAttention || mach.err != "conch isn't installed there" || mach.failures != 1 {
		t.Fatalf("install needed: %v %q", mach.state, mach.err)
	}

	// Success attaches the client and starts listening.
	c := &client.Client{Server: proto.HelloResult{Build: "b1", Capabilities: []string{"agent.install.v1"}}, Events: make(chan proto.Message, 1)}
	mach.sizes["p1"] = [2]int{80, 24}
	gen := mach.gen
	if cmd := mach.connected(machineConnectedMsg{c: c}); cmd == nil {
		t.Fatal("a connection loads the machine's state")
	}
	if mach.c != c || mach.state != stateOnline || mach.err != "" || mach.failures != 0 || mach.gen != gen+1 || len(mach.sizes) != 0 || mach.server.Build != "b1" {
		t.Fatalf("attached: %+v", mach)
	}
	if len(mach.listen()) != 5 || mach.checkAgents() == nil {
		t.Fatal("listen starts five loaders")
	}
	// An outdated server that still answered is used, with a warning.
	mach.connected(machineConnectedMsg{c: c, err: &remote.OutdatedServerError{Missing: []string{"x"}}})
	if mach.state != stateOnline || !strings.Contains(mach.warning, "older build") {
		t.Fatalf("outdated: %v %q", mach.state, mach.warning)
	}

	// Events arrive through waitEvent until the channel closes.
	c.Events <- proto.Message{Event: "pane.exit"}
	if msg, ok := mach.waitEvent()().(machineEventMsg); !ok || msg.machine != "box" || msg.gen != mach.gen || len(msg.msgs) != 1 || msg.msgs[0].Event != "pane.exit" {
		t.Fatalf("event: %#v", msg)
	}
	close(c.Events)
	if msg, ok := mach.waitEvent()().(machineClosedMsg); !ok || msg.err != nil {
		t.Fatalf("closed: %#v", msg)
	}

	// Losing the connection keeps panes and says why.
	mach.panes = []proto.PaneInfo{{ID: "p1"}}
	gen = mach.gen
	mach.lost(client.ErrClosed)
	if mach.c != nil || mach.state != stateOffline || mach.err != "connection lost" || mach.gen != gen+1 || len(mach.panes) != 1 {
		t.Fatalf("lost: %+v", mach)
	}
	mach.lost(errors.New("broken pipe"))
	if mach.err != "connection lost: broken pipe" {
		t.Fatalf("lost with a reason: %q", mach.err)
	}
	if mach.scheduleRetry() == nil {
		t.Fatal("retry")
	}
	gen = mach.gen
	mach.close()
	if mach.gen != gen+1 {
		t.Fatal("close bumps the generation")
	}

	mach.setPanes([]proto.PaneInfo{{ID: "a", Agent: &proto.AgentStatus{Name: "claude"}}, {ID: "b"}})
	if !mach.agents["a"] || mach.agents["b"] || mach.pane("b") == nil || mach.pane("zz") != nil {
		t.Fatalf("panes: %v", mach.agents)
	}
	if mach.missingAgent("claude") {
		t.Fatal("unknown availability is not missing")
	}
	mach.available = map[string]proto.AgentAvailability{"claude": {Installed: true}, "codex": {}}
	if mach.missingAgent("claude") || !mach.missingAgent("codex") || !mach.missingAgent("gemini") {
		t.Fatal("missingAgent")
	}
	mach.setLimits(proto.PlanLimits{Agent: "claude"})
	mach.setLimits(proto.PlanLimits{Agent: "codex"})
	if len(mach.limits) != 2 {
		t.Fatalf("limits %v", mach.limits)
	}
	if addMachine("dev@box", "", "", false) == nil {
		t.Fatal("addMachine returns a command")
	}
}

func TestA2MachineMessages(t *testing.T) {
	m := a2Model()
	box := newMachine("box", "box", "box")
	box.gen = 3
	m.machines = append(m.machines, box)
	m.rebuild()

	// Replies from older connections are ignored.
	m.Update(panesMsg{machine: "box", gen: 2, panes: []proto.PaneInfo{{ID: "x"}}})
	m.Update(limitsMsg{machine: "box", gen: 2, limits: []proto.PlanLimits{{Agent: "claude"}}})
	m.Update(agentStatusMsg{machine: "box", gen: 2, agents: []proto.AgentAvailability{{Name: "claude"}}})
	m.Update(projectsMsg{machine: "box", gen: 2, projects: []proto.ProjectInfo{{ID: "p"}}})
	if box.panes != nil || box.limits != nil || box.available != nil || box.projects != nil {
		t.Fatal("stale replies applied")
	}
	m.Update(panesMsg{machine: "box", gen: 3, panes: []proto.PaneInfo{{ID: "x", Agent: &proto.AgentStatus{Name: "codex"}}}})
	m.Update(limitsMsg{machine: "box", gen: 3, limits: []proto.PlanLimits{{Agent: "claude"}}})
	m.Update(agentStatusMsg{machine: "box", gen: 3, agents: []proto.AgentAvailability{{Name: "claude", Installed: true}}})
	m.Update(projectsMsg{machine: "box", gen: 3, projects: []proto.ProjectInfo{{ID: "p"}}})
	if len(box.panes) != 1 || !box.agents["x"] || len(box.limits) != 1 || !box.available["claude"].Installed || len(box.agentList) != 1 || len(box.projects) != 1 {
		t.Fatalf("replies not applied: %+v", box)
	}

	if _, cmd := m.Update(machineRetryMsg{machine: "box", gen: 2}); cmd != nil {
		t.Fatal("a stale retry")
	}
	if _, cmd := m.Update(machineConnectedMsg{machine: "box", gen: 2}); cmd != nil {
		t.Fatal("a stale connection")
	}
	if _, cmd := m.Update(machineClosedMsg{machine: "box", gen: 2}); cmd != nil || box.state == stateOffline {
		t.Fatal("a stale close")
	}

	// A new connection that needs an install marks the machine's sessions stale.
	m.sessions = map[string]*sessionsData{"box|p": {at: time.Now()}, "local|r1": {at: time.Now()}}
	next, _ := m.Update(machineConnectedMsg{machine: "box", gen: 3, err: &remote.InstallError{Reason: "install"}})
	nm := next.(Model)
	if box.state != stateAttention || !nm.sessions["box|p"].at.IsZero() || nm.sessions["local|r1"].at.IsZero() {
		t.Fatal("connected with an install needed")
	}

	// A closed connection forgets its subscriptions and the pane's frame, but
	// focus stays on the pane so typing is held for it.
	nm.subscribed = map[string]bool{"box|x": true, "local|p1": true}
	nm.frames = map[string]*proto.Frame{"box|x": {}}
	nm.viewMachine, nm.viewing, nm.focus, nm.scrollMode = "box", "x", focusMain, true
	next, cmd := nm.Update(machineClosedMsg{machine: "box", gen: 3, err: errors.New("eof")})
	nm = next.(Model)
	if cmd == nil || box.state != stateOffline || nm.subscribed["box|x"] || nm.frames["box|x"] != nil || nm.viewing != "" || nm.focus != focusMain || nm.scrollMode {
		t.Fatalf("closed: state %v subscribed %v viewing %q focus %v", box.state, nm.subscribed, nm.viewing, nm.focus)
	}
}
