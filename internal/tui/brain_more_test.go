package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/proto"
)

// a2NoProvider configures a brain provider that fails its check without
// running or calling anything.
func a2NoProvider(m *Model) {
	m.cfg.Brain.Provider, m.cfg.Brain.Model = "openai", ""
}

func TestA2AskBarHistory(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.brain = nil
	if m.openAsk() == nil || m.brain == nil {
		t.Fatal("openAsk starts the cursor and brain state")
	}
	b := m.overlay.(*askBar)
	if !b.dimBackground() || b.thinking() {
		t.Fatal("an idle bar dims and doesn't spin")
	}
	// Enter with nothing typed does nothing.
	if _, cmd := b.update(m, a2Key("enter")); cmd != nil || b.phase != askInput {
		t.Fatal("empty request")
	}
	// A provider that can't run reports why and keeps the request.
	a2NoProvider(m)
	a2Type(m, b, "what is waiting")
	b.update(m, a2Key("enter"))
	if !strings.Contains(b.err, "brain not configured") || b.phase != askInput || len(m.brain.history) != 0 {
		t.Fatalf("provider error: %q", b.err)
	}
	out := a2Plain(b.render(*m).lines)
	if !strings.Contains(out, "brain not configured") {
		t.Fatalf("error render:\n%s", out)
	}

	// History browsing.
	m.brain.history = []string{"first", "second"}
	b.hist = 2
	b.update(m, a2Key("up"))
	if b.in.Value() != "second" || b.hist != 1 {
		t.Fatalf("up: %q", b.in.Value())
	}
	b.update(m, a2Key("up"))
	b.update(m, a2Key("up"))
	if b.in.Value() != "first" || b.hist != 0 {
		t.Fatalf("up at the start: %q", b.in.Value())
	}
	b.update(m, a2Key("down"))
	if b.in.Value() != "second" {
		t.Fatalf("down: %q", b.in.Value())
	}
	b.update(m, a2Key("down"))
	if b.in.Value() != "" || b.hist != 2 {
		t.Fatalf("down past the end clears: %q", b.in.Value())
	}
	b.update(m, a2Key("down"))
	if b.hist != 2 {
		t.Fatal("down at the end")
	}
	b.update(m, tickMsg{}) // other messages reach the input
	if closed, _ := b.update(m, a2Key("esc")); !closed || m.overlay != nil {
		t.Fatal("esc closes")
	}
}

func TestA2AskBarSubmitAndCancel(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.cfg.Brain.Provider = "anthropic"
	t.Setenv("ANTHROPIC_API_KEY", "test-key-never-used")
	m.openAsk()
	b := m.overlay.(*askBar)
	m.brain.history = make([]string, 50)
	for i := range m.brain.history {
		m.brain.history[i] = fmt.Sprint("old ", i)
	}
	a2Type(m, b, "fix the tests")
	// The command asks the model, so it is built but never run.
	_, cmd := b.update(m, a2Key("enter"))
	if cmd == nil || b.phase != askThinking || b.cancel == nil || !b.thinking() {
		t.Fatalf("submit: phase %v", b.phase)
	}
	h := m.brain.history
	if len(h) != 50 || h[49] != "fix the tests" || h[0] != "old 1" || b.hist != 50 {
		t.Fatalf("history capped at 50: %d %q %q", len(h), h[0], h[49])
	}
	out := a2Plain(b.render(*m).lines)
	if !strings.Contains(out, "thinking with anthropic") {
		t.Fatalf("thinking render:\n%s", out)
	}
	// Keys other than esc wait; mouse clicks outside don't close while thinking.
	b.update(m, a2Key("x"))
	b.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress}, b.render(*m))
	if b.phase != askThinking || m.overlay == nil {
		t.Fatal("thinking is only cancelled with esc")
	}
	// A plan for another bar, or an error, while thinking.
	b.update(m, planMsg{bar: &askBar{}, plan: brain.Plan{Reply: "no"}})
	if b.phase != askThinking {
		t.Fatal("a plan for another bar")
	}
	b.update(m, a2Key("esc"))
	if b.phase != askInput {
		t.Fatal("esc cancels thinking")
	}
	b.update(m, planMsg{bar: b, plan: brain.Plan{Reply: "late"}})
	if b.phase != askInput {
		t.Fatal("a plan after cancelling is dropped")
	}
	b.phase = askThinking
	b.update(m, planMsg{bar: b, err: errors.New("rate limited")})
	if b.phase != askInput || b.err != "rate limited" {
		t.Fatalf("plan error: %q", b.err)
	}
	// The same request twice is kept once.
	b.err = ""
	b.update(m, a2Key("enter"))
	if n := len(m.brain.history); n != 50 || m.brain.history[49] != "fix the tests" || m.brain.history[48] == "fix the tests" {
		t.Fatalf("repeated request: %v", m.brain.history[47:])
	}
	b.cancel()
}

func TestA2AskBarPlanRunning(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.openAsk()
	b := m.overlay.(*askBar)
	b.in.SetValue("show me the blocked agent and close the shell")
	b.world, b.phase = m.world(), askThinking
	b.world.Machines[0].Online = true
	b.update(m, planMsg{bar: b, plan: brain.Plan{Reply: "Here you go.", Actions: []brain.Action{
		{Type: brain.ActFocus, Machine: localMachine, Pane: "p1"},
		{Type: brain.ActClose, Machine: localMachine, Pane: "p2"},
		{Type: brain.ActStartTask, Machine: localMachine, Project: "r1", Prompt: "Fix it"},
		{Type: brain.ActSend, Machine: localMachine, Pane: "zz", Text: "hi"},
	}}})
	if b.phase != askPlan || len(b.actions) != 4 || b.selectedCount() != 3 || b.actions[3].invalid == "" {
		t.Fatalf("plan: %+v", b.actions)
	}
	// Moving and toggling; invalid actions can't be turned on.
	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"down", 3}, {"down", 3}, {"up", 2}, {"k", 1}, {"up", 0}, {"up", 0}} {
		b.update(m, a2Key(step.key))
		if b.sel != step.want {
			t.Fatalf("after %s sel %d", step.key, b.sel)
		}
	}
	b.sel = 3
	b.update(m, a2Key("x"))
	if b.actions[3].on {
		t.Fatal("an invalid action was turned on")
	}
	b.sel = 2
	b.update(m, a2Key(" "))
	if b.actions[2].on || b.selectedCount() != 2 {
		t.Fatal("space toggles")
	}
	out := a2Plain(b.render(*m).lines)
	for _, want := range []string{"Here you go.", "space toggle · enter run selected", "□", "■", "✗"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan render lacks %q:\n%s", want, out)
		}
	}
	// e goes back to editing, keeping the plan out of the way.
	b.update(m, a2Key("e"))
	if b.phase != askInput {
		t.Fatal("e edits the request")
	}
	b.phase = askPlan

	// Running: nothing is connected, so each selected action fails in turn.
	m.machines[0].c = nil
	_, cmd := b.update(m, a2Key("y"))
	if cmd != nil || b.phase != askDone || !b.actions[0].failed || !b.actions[1].failed || b.actions[2].result != "" {
		t.Fatalf("run offline: phase %v %+v", b.phase, b.actions)
	}
	out = a2Plain(b.render(*m).lines)
	if !strings.Contains(out, "done · any key closes") || !strings.Contains(out, "local is online") {
		t.Fatalf("done render:\n%s", out)
	}

	// Results reported by actions that did run.
	b.phase = askRunning
	for i := range b.actions {
		b.actions[i].result, b.actions[i].failed = "", false
	}
	b.actions[0].result, b.actions[1].result, b.actions[2].result = "…", "…", "…"
	b.update(m, actionDoneMsg{bar: &askBar{}, i: 0})
	b.update(m, a2Key("q"))
	if m.overlay == nil || b.phase != askRunning || b.actions[0].result != "…" {
		t.Fatal("running actions can't be interrupted, and other bars' results are dropped")
	}
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "running…") {
		t.Fatalf("running render:\n%s", out)
	}
	b.update(m, actionDoneMsg{bar: b, i: 0})
	if b.actions[0].result != "shown" {
		t.Fatalf("focus result %q", b.actions[0].result)
	}
	b.update(m, actionDoneMsg{bar: b, i: 1})
	if b.actions[1].result != "done" {
		t.Fatalf("close result %q", b.actions[1].result)
	}
	newPane := proto.PaneInfo{ID: "p9", Name: "claude", Title: "Fixing it", Agent: &proto.AgentStatus{Name: "claude"}}
	b.update(m, actionDoneMsg{bar: b, i: 2, res: brain.Result{Machine: localMachine, Pane: &newPane}})
	if b.actions[2].result != "started Fixing it" || m.machines[0].pane("p9") == nil {
		t.Fatalf("started: %q", b.actions[2].result)
	}
	b.update(m, actionDoneMsg{bar: b, i: 2, err: errors.New("worktree exists")})
	if !b.actions[2].failed || b.actions[2].result != "worktree exists" {
		t.Fatal("action error")
	}
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "✓") || !strings.Contains(out, "worktree exists") {
		t.Fatalf("results render:\n%s", out)
	}
	b.phase = askDone
	if closed, _ := b.update(m, a2Key("z")); !closed || m.overlay != nil {
		t.Fatal("any key closes when done")
	}

	// Enter with nothing selected closes; esc closes a plan; so does a click outside.
	for _, k := range []string{"enter", "esc", "q"} {
		m.overlay = b
		b.phase = askPlan
		for i := range b.actions {
			b.actions[i].on = false
		}
		if closed, _ := b.update(m, a2Key(k)); !closed || m.overlay != nil {
			t.Fatalf("%s should close the plan", k)
		}
	}
	m.overlay = b
	b.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress}, box{lines: []string{"xx"}, x: 50, y: 20})
	if m.overlay != nil {
		t.Fatal("a click outside a plan closes it")
	}

	// An empty plan says so.
	b.actions, b.plan, b.phase = nil, brain.Plan{}, askPlan
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "no actions") || !strings.Contains(out, "e ask something else") {
		t.Fatalf("empty plan:\n%s", out)
	}
	// Very long plans are cut to the screen.
	for i := 0; i < 60; i++ {
		b.actions = append(b.actions, plannedAction{desc: fmt.Sprint("action ", i)})
	}
	short := *m
	short.height = 20
	lines := b.render(short).lines
	if len(lines) > short.height || !strings.Contains(ansi.Strip(lines[len(lines)-2]), "…") {
		t.Fatalf("long plan is %d lines", len(lines))
	}
}

func TestA2AskBarContext(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.openAsk()
	b := m.overlay.(*askBar)
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "context: all machines") {
		t.Fatalf("no selection:\n%s", out)
	}
	m.machines = append(m.machines, &machine{id: "box", label: "box", state: stateOnline, server: proto.HelloResult{Home: "/home/box"}})
	m.rows = []row{
		{id: "pane", kind: kindPane, machine: localMachine, paneID: "p1"},
		{id: "branch", kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"},
		{id: "mach", kind: kindMachine, machine: "box"},
		{id: "proj", kind: kindProject, machine: localMachine, projectID: "r1"},
		{id: "agents", kind: kindAgents, machine: "box"},
	}
	for cursor, want := range map[string]string{
		"pane":   "pane p1 on machine local",
		"branch": "branch feat of project r1 on machine local",
		"mach":   "machine box",
		"proj":   "project r1 on machine local",
		"agents": "",
	} {
		m.cursor = cursor
		if got := m.world().Selected; got != want {
			t.Errorf("cursor %s: %q, want %q", cursor, got, want)
		}
	}
	m.cursor = "branch"
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "context: branch feat of project r1") {
		t.Fatalf("branch context:\n%s", out)
	}
	// Summaries are part of the world.
	m.brain.summaries[paneKey(localMachine, "p1")] = &paneSummary{Summary: brain.Summary{Doing: "Editing"}}
	if w := m.world(); len(w.Machines) != 2 || w.Machines[0].Online {
		t.Fatalf("world: %+v", w.Machines)
	}
}

func TestA2Summaries(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.brain = nil
	p := *m.machines[0].pane("p1")
	// No connection: nothing to read.
	if m.summarize(localMachine, p, true) != nil || m.brain == nil {
		t.Fatal("summarize without a connection")
	}
	key := paneKey(localMachine, "p1")
	m.brain.summaries[key] = &paneSummary{pending: true}
	if m.summarize(localMachine, p, true) != nil {
		t.Fatal("a pending summary is not requested again")
	}
	delete(m.brain.summaries, key)
	m.brain.inflight = maxSummaries
	if m.summarize(localMachine, p, false) != nil {
		t.Fatal("automatic summaries respect the in-flight limit")
	}
	m.brain.inflight = 0
	m.machines[0].c = a2Client()
	a2NoProvider(m)
	if m.summarize(localMachine, p, false) != nil || m.flash != "" {
		t.Fatal("automatic summaries fail quietly")
	}
	if m.summarize(localMachine, p, true) != nil || !strings.Contains(m.flash, "brain not configured") {
		t.Fatalf("manual summaries say why: %q", m.flash)
	}
	// A working provider: the request reads the pane, so it is built but not run.
	m.cfg.Brain.Provider = "anthropic"
	t.Setenv("ANTHROPIC_API_KEY", "test-key-never-used")
	if m.summarize(localMachine, p, false) == nil || !m.brain.summaries[key].pending || m.brain.inflight != 1 {
		t.Fatal("summary requested")
	}
	m.receiveSummary(summaryMsg{key: key, err: errors.New("overloaded")})
	if s := m.brain.summaries[key]; s.pending || s.err != "overloaded" || m.flash != "summary: overloaded" || m.brain.inflight != 0 {
		t.Fatalf("summary error: %+v", s)
	}
	m.receiveSummary(summaryMsg{key: "gone"})
	if m.brain.inflight != 0 {
		t.Fatal("inflight never goes negative")
	}
	m.receiveSummary(summaryMsg{key: key, summary: brain.Summary{Doing: "Running tests"}})
	if m.summaryText(localMachine, "p1") != "Running tests" {
		t.Fatalf("doing: %q", m.summaryText(localMachine, "p1"))
	}
	var nobrain Model
	nobrain.receiveSummary(summaryMsg{key: key})

	// Automatic summaries on state changes.
	m.cfg.Brain.Summaries = true
	m.brain.summaries[key].pending = true // keep requests from being built
	working := p
	working.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}
	idle := p
	idle.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}
	if m.observeAgent(m.machines[0], working, working) != nil || m.observeAgent(m.machines[0], p, proto.PaneInfo{ID: "p1"}) != nil {
		t.Fatal("no change, no agent")
	}
	delete(m.brain.summaries, key)
	if m.observeAgent(m.machines[0], working, idle) == nil {
		t.Fatal("an agent that finished is summarised")
	}
	delete(m.brain.summaries, key)
	m.brain.inflight = 0
	if m.observeAgent(m.machines[0], proto.PaneInfo{ID: "p1"}, p) == nil {
		t.Fatal("an agent waiting for the user is summarised")
	}
	if m.observeAgent(m.machines[0], idle, working) != nil {
		t.Fatal("starting work is not summarised")
	}

	// S on the tree.
	m.rows = []row{{id: "proj", kind: kindProject, machine: localMachine, projectID: "r1"},
		{id: "p2", kind: kindPane, machine: localMachine, paneID: "p2"}, {id: "p1", kind: kindPane, machine: localMachine, paneID: "p1"}}
	m.cursor = "proj"
	if m.summarizeSelected() != nil || m.flash != "select an agent to summarise" {
		t.Fatalf("S on a project: %q", m.flash)
	}
	m.cursor = "p2"
	if m.summarizeSelected() != nil || m.flash != "no agent is running in this pane" {
		t.Fatalf("S on a shell: %q", m.flash)
	}
	m.cursor = "p1"
	delete(m.brain.summaries, key)
	if m.summarizeSelected() == nil || m.flash != "summarising claude…" {
		t.Fatalf("S on an agent: %q", m.flash)
	}
	m.machines[0].c = nil
	delete(m.brain.summaries, key)
	m.flash = ""
	if m.summarizeSelected() != nil || m.flash != "" {
		t.Fatal("S without a connection")
	}
}
