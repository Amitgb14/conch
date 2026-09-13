package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestAskBarPlanFlow(t *testing.T) {
	m := &Model{cfg: config.Default(), width: 110, height: 40, brain: newBrainState()}
	claude := proto.AgentAvailability{Name: "claude", Installed: true}
	m.machines = []*machine{{id: localMachine, label: "local", state: stateOnline, agentList: []proto.AgentAvailability{claude},
		projects: []proto.ProjectInfo{{ID: "r1", Name: "api", Path: "/src/api", Git: true}},
		panes:    []proto.PaneInfo{{ID: "p1", Name: "claude", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude", State: "blocked"}}}}}

	m.openAsk()
	b := m.overlay.(*askBar)
	b.in.SetValue("fix the flaky tests")
	b.world, b.phase = m.world(), askThinking
	b.world.Machines[0].Online = true // a connected client in real use

	plan := brain.Plan{Reply: "Two tasks.", Actions: []brain.Action{
		{Type: brain.ActStartTask, Machine: localMachine, Project: "r1", Prompt: "Fix TestLogin"},
		{Type: brain.ActStartTask, Machine: localMachine, Project: "nope", Prompt: "x"},
	}}
	b.update(m, planMsg{bar: b, plan: plan})
	if b.phase != askPlan || len(b.actions) != 2 || !b.actions[0].on || b.actions[1].invalid == "" {
		t.Fatalf("plan state: %+v", b.actions)
	}
	out := ansi.Strip(strings.Join(b.render(*m).lines, "\n"))
	for _, want := range []string{"Two tasks.", "Start claude in api on local", "unknown project"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}

	// Toggling off the only valid action leaves nothing to run: enter closes.
	b.update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if b.actions[0].on || b.selectedCount() != 0 {
		t.Fatal("space should toggle the action off")
	}
	b.update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.overlay != nil {
		t.Fatal("enter with nothing selected should close the bar")
	}

	// Running without a connection reports the failure per action.
	m.openAsk()
	b = m.overlay.(*askBar)
	b.world, b.phase = m.world(), askThinking
	b.world.Machines[0].Online = true
	b.update(m, planMsg{bar: b, plan: brain.Plan{Actions: plan.Actions[:1]}})
	b.update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if b.phase != askDone || !b.actions[0].failed {
		t.Fatalf("offline run: phase %v %+v", b.phase, b.actions[0])
	}
}

func TestSummaries(t *testing.T) {
	m := &Model{cfg: config.Default(), brain: newBrainState()}
	p := proto.PaneInfo{ID: "p1", Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}}
	mach := &machine{id: localMachine}
	// Off by default: no request when an agent starts waiting.
	blocked := p
	blocked.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}
	if m.observeAgent(mach, p, blocked) != nil {
		t.Fatal("summaries are off by default")
	}
	key := paneKey(localMachine, "p1")
	m.brain.summaries[key] = &paneSummary{pending: true}
	m.brain.inflight = 1
	m.receiveSummary(summaryMsg{key: key, summary: brain.Summary{Doing: "Editing auth.go", Needs: "Approve go test"}})
	if got := m.summaryText(localMachine, "p1"); got != "needs: Approve go test" || m.brain.inflight != 0 {
		t.Fatalf("summary text %q inflight %d", got, m.brain.inflight)
	}
	var nilBrain Model
	if nilBrain.summaryText(localMachine, "p1") != "" {
		t.Fatal("a model without brain state has no summaries")
	}
}
