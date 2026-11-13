package tui

import (
	"strings"
	"testing"

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
