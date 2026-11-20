package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// endedFixture is a1Fixture on a server that keeps ended agents (or not).
func endedFixture(t *testing.T, keeps bool) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	var caps []string
	if keeps {
		caps = []string{proto.CapPaneEnded}
	}
	c, peer := a1FakeClient(t, caps...)
	mach := m.machines[0]
	mach.c, mach.server = c, c.Server
	return m, mach, peer
}

func endedRec(id, pane string) proto.EndedAgent {
	return proto.EndedAgent{ID: id, Pane: pane, Agent: "claude", Dir: "/src/api", Name: "Release planning", ProjectID: "r1",
		Why: proto.EndedExited, ExitCode: 1, Screen: []string{"last words"}, Started: time.Now().Add(-time.Hour), Ended: time.Now().Add(-3 * time.Minute)}
}

func rowText(m *Model, id string) string {
	i := indexOfRow(m.rows, id)
	if i < 0 {
		return ""
	}
	return ansi.Strip(m.rowLine(m.rows[i], m.sidebarW))
}

// TestEndedAgentRow: an ended agent whose pane is gone is a row of its own
// where it was, saying why and offering Resume, within the sidebar's
// width — and in a split, its last screen. One in a project the tree does
// not show is left out.
func TestEndedAgentRow(t *testing.T) {
	m, mach, _ := endedFixture(t, true)
	m.Update(endedMsg{machine: localMachine, gen: mach.gen, ended: []proto.EndedAgent{
		endedRec("p7.a", "p7"), {ID: "x.b", Pane: "x", Agent: "codex", ProjectID: "elsewhere", Why: proto.EndedServer, Ended: time.Now()},
	}})
	m.rebuild()
	id := paneNodeID(localMachine, endedPrefix+"p7.a")
	if i := indexOfRow(m.rows, id); i < 0 || m.rows[i].projectID != "r1" {
		t.Fatalf("no ended row under r1: %s", render(m.rows))
	}
	if indexOfRow(m.rows, paneNodeID(localMachine, endedPrefix+"x.b")) >= 0 {
		t.Fatal("a project the tree does not show")
	}
	for _, w := range []int{60, 30, 12} {
		m.sidebarW = w
		line := m.rowLine(m.rows[indexOfRow(m.rows, id)], w)
		if ansi.StringWidth(line) > w {
			t.Fatalf("row %d wide in %d: %q", ansi.StringWidth(line), w, line)
		}
	}
	m.sidebarW = 80
	if got := rowText(m, id); !strings.Contains(got, "Release planning") || !strings.Contains(got, "ended 3m ago") || !strings.Contains(got, "Resume") {
		t.Fatalf("row: %q", got)
	}
	a1At(t, m, id)
	if m.subscribed[paneKey(localMachine, endedPrefix+"p7.a")] {
		t.Fatal("subscribed to a pane that does not exist")
	}
	body := ansi.Strip(strings.Join(m.leafBody(&leaf{view: viewRef{Row: id, Kind: kindPane, Machine: localMachine, PaneID: endedPrefix + "p7.a"}}, 60, 10, false), "\n"))
	if !strings.Contains(body, "last words") || !strings.Contains(body, "enter resumes") {
		t.Fatalf("split: %q", body)
	}
	tiny := m.endedLines(endedRec("p7.a", "p7"), 5, 1)
	for _, l := range tiny {
		if ansi.StringWidth(l) > 5 {
			t.Fatalf("too wide at 5: %q", l)
		}
	}
}

// TestEndedPaneIsKept: an agent's pane the server keeps as ended stays
// open on its last screen rather than being closed; without a record —
// or from a server without pane.ended.v1 — it closes as before.
func TestEndedPaneIsKept(t *testing.T) {
	m, mach, peer := endedFixture(t, true)
	m.handleEvent(mach, eventMsg(t, proto.EventPaneEnded, endedRec("p1.a", "p1")))
	exited := mach.panes[0]
	exited.State, exited.Agent, exited.Created = proto.PaneExited, nil, time.Now().Add(-time.Hour)
	a2Run(m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, exited)))
	if n := peer.count(t, mach.c, proto.MethodPaneClose, `"p1"`); n != 0 {
		t.Fatalf("an ended agent's pane was closed %d times", n)
	}
	m.sidebarW = 80
	m.rebuild()
	id := paneNodeID(localMachine, "p1")
	if got := rowText(m, id); !strings.Contains(got, "ended 3m ago · Resume") {
		t.Fatalf("its own row is the ended one: %q", got)
	}
	if got := m.leafTitle(&leaf{view: viewRef{Row: id, Kind: kindPane, Machine: localMachine, PaneID: "p1"}}); !strings.Contains(got, "exited with code 1") {
		t.Fatalf("title says why: %q", got)
	}
	if indexOfRow(m.rows, paneNodeID(localMachine, endedPrefix+"p1.a")) >= 0 {
		t.Fatal("listed twice")
	}

	// A live pane that took the id of an older record is not it.
	m.handleEvent(mach, eventMsg(t, proto.EventPaneEnded, endedRec("p4.old", "p4")))
	if m.endedFor(localMachine, "p4") != nil {
		t.Fatal("a running pane taken for an ended record")
	}

	// No record: closed as before.
	shell := mach.panes[1]
	shell.State, shell.Created = proto.PaneExited, time.Now().Add(-time.Hour)
	a2Run(m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, shell)))
	peer.waitMethod(t, proto.MethodPaneClose, `"p2"`)

	// An older server: nothing loaded, nothing asked.
	old, omach, _ := endedFixture(t, false)
	if omach.loadEnded() != nil {
		t.Fatal("asked an older server for ended agents")
	}
	_ = old
}

// TestEndedKeys: enter or r resumes an ended agent, d or x dismisses it —
// gone from the tree at once — and other keys are the row's as before.
func TestEndedKeys(t *testing.T) {
	m, mach, peer := endedFixture(t, true)
	m.Update(endedMsg{machine: localMachine, gen: mach.gen, ended: []proto.EndedAgent{endedRec("p7.a", "p7"), endedRec("p8.b", "p8")}})
	m.rebuild()
	m.focus = focusSidebar

	a1At(t, m, paneNodeID(localMachine, endedPrefix+"p7.a"))
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	peer.waitMethod(t, proto.MethodPaneEndedResume, `"p7.a"`)

	a1At(t, m, paneNodeID(localMachine, endedPrefix+"p8.b"))
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}))
	peer.waitMethod(t, proto.MethodPaneEndedDismiss, `"p8.b"`)
	if indexOfRow(m.rows, paneNodeID(localMachine, endedPrefix+"p8.b")) >= 0 {
		t.Fatal("dismissed row still listed")
	}

	// r on an ordinary pane still renames it.
	a1At(t, m, paneNodeID(localMachine, "p1"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.overlay == nil {
		t.Fatal("r on a live pane no longer renames")
	}
	if n := peer.count(t, mach.c, proto.MethodPaneEndedResume, `"p1"`); n != 0 {
		t.Fatal("a live pane resumed")
	}
}

// TestEndedResumedInPlace: the agent comes back in the split its ended
// pane was in, whichever of the server's event and its reply comes first,
// and is not shown twice.
func TestEndedResumedInPlace(t *testing.T) {
	for _, eventFirst := range []bool{true, false} {
		m, mach, _ := endedFixture(t, true)
		a1Open(t, m, paneNodeID(localMachine, "p2"))
		m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p1"))]))
		rec := endedRec("p1.a", "p1")
		mach.addEnded(rec)
		back := proto.PaneInfo{ID: "p9", Name: "Release planning", State: proto.PaneRunning, ProjectID: "r1",
			Agent: &proto.AgentStatus{Name: "claude"}}
		mach.panes = append(mach.panes, back)
		gone := eventMsg(t, proto.EventPaneEndedGone, proto.EndedGone{ID: rec.ID, Pane: "p1", Resumed: "p9"})
		if eventFirst {
			m.handleEvent(mach, gone)
			m.Update(endedResumedMsg{machine: localMachine, pane: "p1", info: back})
		} else {
			next, _ := m.Update(endedResumedMsg{machine: localMachine, pane: "p1", info: back})
			*m = next.(Model)
			m.handleEvent(mach, gone)
		}
		m.rebuild()
		var shown []string
		for _, t := range m.tabs {
			for _, l := range t.root.leaves() {
				shown = append(shown, l.view.PaneID)
			}
		}
		if strings.Join(shown, ",") != "p2,p9" {
			t.Fatalf("event first %v: shown %v", eventFirst, shown)
		}
		if len(mach.ended) != 0 {
			t.Fatalf("record kept: %+v", mach.ended)
		}
	}
}
