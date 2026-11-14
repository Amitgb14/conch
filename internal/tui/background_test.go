package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent that finished its turn but left something running says so on
// its row: idle ⟳2, not plain idle.
func TestTreeShowsBackgroundJobs(t *testing.T) {
	m := a5Helpers(t)
	row := func(id string) string {
		i := indexOfRow(m.rows, paneNodeID(localMachine, id))
		if i < 0 {
			t.Fatalf("no row for %s", id)
		}
		_, _, label, _, right := m.rowParts(m.rows[i])
		return ansi.Strip(label + " " + right)
	}
	if got := row("p9"); strings.Contains(got, "⟳") {
		t.Fatalf("no jobs, no mark: %q", got)
	}
	p9 := &m.machines[0].panes[len(m.machines[0].panes)-1]
	p9.Agent.Background = 2
	p9.Agent.Jobs = []proto.BackgroundJob{{PID: 5, Command: "npm run dev"}, {PID: 6, Command: "pytest -f"}}
	if got := row("p9"); !strings.Contains(got, "⟳2") {
		t.Fatalf("two jobs: %q", got)
	}
	// The count is what the server said, even from one that sent no list.
	p9.Agent.Jobs = nil
	if got := row("p9"); !strings.Contains(got, "⟳2") {
		t.Fatalf("count without details: %q", got)
	}
	p9.Agent.Background = 120
	if got := row("p9"); !strings.Contains(got, "⟳120") {
		t.Fatalf("a count past two digits: %q", got)
	}
	// An exited pane runs nothing.
	p9.State = proto.PaneExited
	if got := row("p9"); strings.Contains(got, "⟳") {
		t.Fatalf("exited: %q", got)
	}
}

func TestBackgroundSummary(t *testing.T) {
	for _, c := range []struct {
		a    *proto.AgentStatus
		want string
	}{
		{nil, ""},
		{&proto.AgentStatus{}, ""},
		{&proto.AgentStatus{Background: 1, Jobs: []proto.BackgroundJob{{Command: "npm run dev"}}}, "1 still running (npm run dev)"},
		{&proto.AgentStatus{Background: 3, Jobs: []proto.BackgroundJob{{Command: "npm run dev"}, {Command: "pytest -f"}, {Command: "x"}}},
			"3 still running (npm run dev, pytest -f, …)"},
		{&proto.AgentStatus{Background: 2}, "2 still running"},
		{&proto.AgentStatus{Background: 1, Jobs: []proto.BackgroundJob{{Command: strings.Repeat("y", 100)}}},
			"1 still running (" + strings.Repeat("y", 39) + "…)"},
	} {
		if got := backgroundSummary(c.a); got != c.want {
			t.Errorf("backgroundSummary(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

// The notification that an agent finished, or is waiting, says what it
// left running: "done" alone would read as quiet.
func TestAttentionBodySaysWhatIsStillRunning(t *testing.T) {
	jobs := []proto.BackgroundJob{{PID: 9, Command: "npm run dev"}}
	for _, c := range []struct {
		a    proto.AgentStatus
		want string
	}{
		{proto.AgentStatus{State: proto.AgentDone, Background: 1, Jobs: jobs}, "w finished, 1 still running (npm run dev)"},
		{proto.AgentStatus{State: proto.AgentBlocked, Background: 1, Jobs: jobs}, "w is waiting for you, 1 still running (npm run dev)"},
		{proto.AgentStatus{State: proto.AgentBlocked, Message: "Allow Bash?", Background: 1, Jobs: jobs}, "Allow Bash? · 1 still running (npm run dev)"},
		{proto.AgentStatus{State: proto.AgentDone, Failed: true, Message: "boom", Background: 2}, "w stopped: boom, 2 still running"},
		{proto.AgentStatus{State: proto.AgentDone}, "w finished"},
	} {
		a := c.a
		info := proto.PaneInfo{Name: "w", State: proto.PaneRunning, Agent: &a}
		if got := attentionBody(info); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}

// What it left running is listed under the agent, as its subagents are:
// the command, its ports and how long it has run, a row each.
func TestTreeListsJobsUnderTheAgent(t *testing.T) {
	in := sampleInput()
	a := in.machines[0].panes[0].Agent
	a.State, a.Background = proto.AgentDone, 2
	a.Subagents = []proto.Subagent{{ID: "s1"}}
	a.Jobs = []proto.BackgroundJob{{PID: 41, Command: "npm run dev", Ports: []int{5173}}, {PID: 42, Command: "pytest -f"}}
	rows := buildTree(in)
	got := render(rows)
	want := "        pane:p1\n          " + subagentID(localMachine, "p1", "s1") + "\n          " + jobID(localMachine, "p1", 41) +
		"\n          " + jobID(localMachine, "p1", 42) + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("not under the agent:\n%s\nwant\n%s", got, want)
	}
	r := rows[indexOfRow(rows, jobID(localMachine, "p1", 41))]
	if r.kind != kindJob || r.paneID != "p1" || r.label != "41" || !r.nested || r.expandable() {
		t.Fatalf("job row %+v", r)
	}
	if rows[indexOfRow(rows, paneNodeID(localMachine, "p1"))].kids != 3 {
		t.Fatal("the agent's row counts its jobs")
	}
	// The count alone, from a server that sent no list: no rows to make.
	a.Jobs = nil
	if strings.Contains(render(buildTree(in)), "/job/") {
		t.Fatal("rows without jobs")
	}
}

func TestJobRow(t *testing.T) {
	m, peer := monitorFixture(t, proto.CapAgentBackground)
	p := &m.machines[0].panes[0]
	p.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentIdle, Background: 1,
		Jobs: []proto.BackgroundJob{{PID: 41, Command: "npm run dev", Ports: []int{5173, 8080}, Since: time.Now().Add(-3 * time.Minute)}}}
	m.rebuild()
	i := indexOfRow(m.rows, jobID(localMachine, p.ID, 41))
	if i < 0 {
		t.Fatalf("no job row in\n%s", render(m.rows))
	}
	g, _, label, _, right := m.rowParts(m.rows[i])
	if g != "⟳" || label != "npm run dev" || ansi.Strip(right) != ":5173 :8080 3m" {
		t.Fatalf("job row: %q %q %q", g, label, ansi.Strip(right))
	}
	// A job the server no longer lists draws as gone, not as a crash.
	if _, _, label, _, _ := m.rowParts(row{kind: kindJob, machine: localMachine, paneID: p.ID, label: "99"}); label != "ended" {
		t.Fatalf("a job that has gone: %q", label)
	}

	// Its menu stops it, through the server.
	m.cursor = m.rows[i].id
	menu := newRowMenu(*m, m.rows[i], 0, 0)
	var stop *menuItem
	for k := range menu.items {
		if strings.HasPrefix(menu.items[k].label, "Stop") {
			stop = &menu.items[k]
		}
	}
	if stop == nil {
		t.Fatalf("no stop in %+v", menu.items)
	}
	cmd := stop.run(m)
	if cmd == nil {
		t.Fatal("no call")
	}
	cmd()
	peer.waitMethod(t, proto.MethodAgentBackgroundStop, `{"id":"`+p.ID+`","pid":41}`)
	if !strings.Contains(m.flash, "npm run dev") {
		t.Fatalf("flash %q", m.flash)
	}

	// Enter on it goes to its agent, which is what has a screen.
	if a := m.agentRowOf(m.rows[i]); a.kind != kindPane || a.paneID != p.ID {
		t.Fatalf("its agent's row: %+v", a)
	}
}

func TestStopJobOldServer(t *testing.T) {
	m, _ := monitorFixture(t, "pane.v1")
	if m.stopJob(localMachine, m.machines[0].panes[0].ID, 41) != nil {
		t.Fatal("called a server without agent.background")
	}
	if !m.flashIsErr || !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash %q", m.flash)
	}
	if m.stopJob("elsewhere", "p1", 41) != nil {
		t.Fatal("an unknown machine")
	}
}
