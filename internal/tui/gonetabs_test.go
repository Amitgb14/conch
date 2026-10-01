package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestGonePanesTakeTheirTabsWithThem: a pane that exits while the TUI is
// running closes its tab on the event, but one that goes while the TUI is
// closed — a server restarted, a shell ended, a pane closed from the
// command line — used to leave a tab behind labelled "empty", and they
// piled up across sessions. A full pane list is when to notice.
func TestGonePanesTakeTheirTabsWithThem(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		a1Open(t, m, paneNodeID(localMachine, id))
	}
	if len(m.tabs) != 4 {
		t.Fatalf("tabs: %d", len(m.tabs))
	}

	// The server now has only p1 and p3: the others went while we were away.
	mach.setPanes([]proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}},
		{ID: "p3", Name: "bash", State: proto.PaneRunning},
	})
	m.forgetGonePanes(mach)

	var left []string
	for _, tb := range m.tabs {
		for _, l := range tb.root.leaves() {
			if l.view.Kind == kindPane {
				left = append(left, l.view.PaneID)
			}
		}
	}
	if got := strings.Join(left, ","); got != "p1,p3" {
		t.Fatalf("tabs left showing %q, want p1,p3", got)
	}
	// Nothing is labelled "empty" any more.
	for _, tb := range m.tabs {
		if l := ansi.Strip(m.tabLabel(tb)); strings.Contains(l, "empty") {
			t.Fatalf("a tab is still %q", l)
		}
	}
	// A pane the server still lists as exited is kept: conch keeps a failed
	// launch on screen so its error can be read.
	mach.setPanes([]proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneExited, Agent: &proto.AgentStatus{Name: "claude"}},
		{ID: "p3", Name: "bash", State: proto.PaneRunning},
	})
	m.forgetGonePanes(mach)
	if len(m.tabs) != 2 {
		t.Fatalf("an exited pane lost its tab: %d tabs", len(m.tabs))
	}
	// Another machine's panes are none of this machine's business.
	other := newMachine("box", "box", "")
	other.state = stateOnline
	other.panes = []proto.PaneInfo{{ID: "p1", Name: "zsh", State: proto.PaneRunning}}
	m.machines = append(m.machines, other)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	before := len(m.tabs)
	m.forgetGonePanes(other)
	if len(m.tabs) != before {
		t.Fatalf("another machine's list closed this machine's tabs: %d, was %d", len(m.tabs), before)
	}
}
