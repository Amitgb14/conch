package tui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
)

// hostSaw is what conch told the terminal since the last call, emptied
// for the next step.
func hostSaw() []string {
	out := hostReports
	hostReports = nil
	return out
}

// agentsIn sets the fixture's four panes to the states named, by pane id.
func agentsIn(m *Model, states map[string]*proto.AgentStatus) {
	for i := range m.machines[0].panes {
		p := &m.machines[0].panes[i]
		if a, ok := states[p.ID]; ok {
			p.Agent = a
		} else {
			p.Agent = nil
		}
	}
}

// TestHostStatusPrecedence: what conch says about itself, and in what
// order. Waiting comes first because it is the only state that needs
// somebody this second; a failed agent next, because it is finished and
// wrong and no later report will mention it.
func TestHostStatusPrecedence(t *testing.T) {
	m, _ := a1Fixture(t, false)
	blocked := &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}
	working := &proto.AgentStatus{Name: "codex", State: proto.AgentWorking}
	failed := &proto.AgentStatus{Name: "gemini", State: proto.AgentDone, Failed: true}
	done := &proto.AgentStatus{Name: "opencode", State: proto.AgentDone}
	idle := &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}

	for _, c := range []struct {
		name   string
		states map[string]*proto.AgentStatus
		want   string
	}{
		{"nothing at all", nil, "state=idle:app=conch"},
		{"one idle", map[string]*proto.AgentStatus{"p1": idle}, "state=idle:app=conch"},
		{"one working", map[string]*proto.AgentStatus{"p1": working},
			"state=working:app=conch:msg=" + b64tui("1 agent working")},
		{"two working", map[string]*proto.AgentStatus{"p1": working, "p4": working},
			"state=working:app=conch:msg=" + b64tui("2 agents working")},
		{"done", map[string]*proto.AgentStatus{"p1": done}, "state=done:app=conch:msg=" + b64tui("1 agent done")},
		// A failure beats work still going on: nothing later will mention it.
		{"failed beats working", map[string]*proto.AgentStatus{"p1": failed, "p4": working},
			"state=error:app=conch:msg=" + b64tui("claude on feat failed")},
		// And waiting beats everything, including a failure.
		{"waiting beats all", map[string]*proto.AgentStatus{"p1": blocked, "p3": failed, "p4": working},
			"state=blocked:kind=question:app=conch:msg=" + b64tui("claude on feat waiting")},
		// Named when there is one of it, counted when there are more.
		{"two waiting", map[string]*proto.AgentStatus{"p1": blocked, "p4": blocked},
			"state=blocked:kind=question:app=conch:msg=" + b64tui("2 agents waiting")},
	} {
		agentsIn(m, c.states)
		if got := pane.FormatStatus(m.hostStatus()); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}

	// A pane with no branch is named on its own: the fixture's p4 is a
	// machine's own pane.
	agentsIn(m, map[string]*proto.AgentStatus{"p4": blocked})
	if got := m.hostStatus().Msg; got != "codex waiting" {
		t.Errorf("a pane with no branch: %q", got)
	}
}

// TestHostStatusCountsEveryWorkspace: the report is about conch, not
// about the workspace on screen. The ⚑ counter in the status bar counts
// only what the workspace shows; this deliberately does not, because the
// terminal's tab is outside workspaces and an agent waiting in one you
// have switched away from is the one you cannot see.
func TestHostStatusCountsEveryWorkspace(t *testing.T) {
	m, _ := a1Fixture(t, false)
	agentsIn(m, map[string]*proto.AgentStatus{"p1": {Name: "claude", State: proto.AgentBlocked}})
	m.newSpace() // an empty workspace: it shows none of them
	m.rebuild()
	if waiting, _ := m.inboxCount(); waiting != 0 {
		t.Fatalf("this test needs a workspace that shows nothing; the bar counts %d", waiting)
	}
	rec := m.hostStatus()
	if rec.State != pane.StatusBlocked || rec.Msg != "claude on feat waiting" {
		t.Fatalf("the terminal was told %+v", rec)
	}
}

// TestHostStatusLeavesItselfOut: a conch run inside a conch pane is
// detected as an agent there, and that pane's state is this very report
// read back. Counting it made conch say "2 agents waiting" about one
// waiting agent and itself — found by running the TUI in a pane of a
// scratch conch, where it latched at two.
func TestHostStatusLeavesItselfOut(t *testing.T) {
	m, _ := a1Fixture(t, false)
	blocked := &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}
	agentsIn(m, map[string]*proto.AgentStatus{"p1": blocked, "p4": blocked})
	m.machines[0].ownPane = "p4" // conch is running in p4
	rec := m.hostStatus()
	if rec.State != pane.StatusBlocked || rec.Msg != "claude on feat waiting" {
		t.Fatalf("counted itself: %+v", rec)
	}
	// And with nothing but itself, conch is idle rather than waiting on
	// its own report.
	agentsIn(m, map[string]*proto.AgentStatus{"p4": blocked})
	if rec := m.hostStatus(); rec.State != pane.StatusIdle || rec.Msg != "" {
		t.Fatalf("alone in a pane: %+v", rec)
	}
}

// TestHostStatusSpeaksOnChange: a program reports when something changes
// and not otherwise — the rule conch itself relies on when it is the one
// reading, and the reason `done` is raised once rather than for ever.
func TestHostStatusSpeaksOnChange(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.hostStatusOn = true
	hostSaw()

	step := func(t *testing.T) *Model {
		t.Helper()
		next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		nm := next.(Model)
		return &nm
	}
	agentsIn(m, map[string]*proto.AgentStatus{"p1": {Name: "claude", State: proto.AgentWorking}})
	m = step(t)
	if got := hostSaw(); len(got) != 1 || !strings.HasPrefix(got[0], "state=working:") {
		t.Fatalf("first report: %q", got)
	}
	// Nothing has changed: nothing is said.
	m = step(t)
	if got := hostSaw(); len(got) != 0 {
		t.Fatalf("said it again with nothing changed: %q", got)
	}
	// The agent stops to ask: said once.
	agentsIn(m, map[string]*proto.AgentStatus{"p1": {Name: "claude", State: proto.AgentBlocked}})
	m = step(t)
	m = step(t)
	got := hostSaw()
	if len(got) != 1 || !strings.HasPrefix(got[0], "state=blocked:kind=question:") {
		t.Fatalf("after it stopped to ask: %q", got)
	}

	// Quitting takes the report away: a tab still saying an agent waits
	// would outlive the conch that said so.
	m.quitting()
	if got := hostSaw(); len(got) != 1 || got[0] != "state=clear" {
		t.Fatalf("on quit: %q", got)
	}
	// And a second quit says nothing: there is nothing left to clear.
	m.quitting()
	if got := hostSaw(); len(got) != 0 {
		t.Fatalf("cleared twice: %q", got)
	}
}

// With the reports off nothing is written at all, whatever happens.
func TestHostStatusOffWritesNothing(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.hostStatusOn = false
	hostSaw()
	agentsIn(m, map[string]*proto.AgentStatus{"p1": {Name: "claude", State: proto.AgentBlocked}})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	nm := next.(Model)
	nm.quitting()
	if got := hostSaw(); len(got) != 0 {
		t.Fatalf("off, and still wrote %q", got)
	}
}

// TestHostStatusWorks: who gets told. conch cannot ask the terminal
// whether it speaks the protocol — the answer would arrive on stdin as
// keystrokes — so this decides, and $CONCH_STATUS settles it either way.
func TestHostStatusWorks(t *testing.T) {
	real := stdoutIsTerminal
	defer func() { stdoutIsTerminal = real }()
	for _, c := range []struct {
		name, conch, tmux, sty string
		tty                    bool
		want                   bool
	}{
		{name: "a terminal", tty: true, want: true},
		{name: "output to a file", tty: false, want: false},
		{name: "inside tmux", tmux: "/tmp/tmux-501/default,1,0", tty: true, want: false},
		{name: "inside screen", sty: "1234.pts-0", tty: true, want: false},
		{name: "asked for anyway", conch: "1", tmux: "x", tty: false, want: true},
		{name: "turned off", conch: "0", tty: true, want: false},
		{name: "off by word", conch: "false", tty: true, want: false},
		{name: "nonsense is no answer", conch: "maybe", tty: true, want: true},
	} {
		t.Setenv("CONCH_STATUS", c.conch)
		t.Setenv("TMUX", c.tmux)
		t.Setenv("STY", c.sty)
		stdoutIsTerminal = func() bool { return c.tty }
		if got := hostStatusWorks(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// b64tui encodes a message the way a report carries one.
func b64tui(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
