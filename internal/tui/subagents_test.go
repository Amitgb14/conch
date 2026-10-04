package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestBuildTreeListsSubagents: the agents a Claude runs in its own process
// are listed under it, after the panes it started, and fold away with them.
func TestBuildTreeListsSubagents(t *testing.T) {
	in := sampleInput()
	ps := in.machines[0].panes
	ps[0].Agent.Subagents = []proto.Subagent{{ID: "a1", Type: "general-purpose"}, {ID: "a2", Type: "Explore"}}
	in.machines[0].panes = append(ps, proto.PaneInfo{ID: "p4", Name: "reviewer", ProjectID: "r1", State: proto.PaneRunning,
		CreatedBy: "p1", Agent: &proto.AgentStatus{Name: "claude"}})
	rows := buildTree(in)
	got := render(rows)
	sub := func(id string) string { return subagentID(localMachine, "p1", id) }
	want := "      p:r1/agents\n        pane:p1\n          pane:p4\n          " + sub("a1") + "\n          " + sub("a2") + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("not listed under the agent:\n%s\nwant\n%s", got, want)
	}
	i := indexOfRow(rows, paneNodeID(localMachine, "p1"))
	if r := rows[i]; r.kids != 3 || !r.expandable() {
		t.Fatalf("the agent's row counts helpers and subagents: %+v", r)
	}
	if r := rows[indexOfRow(rows, sub("a1"))]; r.kind != kindSubagent || r.paneID != "p1" || r.label != "a1" ||
		!r.nested || r.expandable() || r.projectID != "r1" {
		t.Fatalf("subagent row: %+v", r)
	}
	if parentID(rows, indexOfRow(rows, sub("a2"))) != paneNodeID(localMachine, "p1") {
		t.Fatal("left from a subagent goes to its agent")
	}

	// Subagents alone make an agent foldable, and folded they are hidden.
	in.machines[0].panes = in.machines[0].panes[:3]
	rows = buildTree(in)
	if r := rows[indexOfRow(rows, paneNodeID(localMachine, "p1"))]; r.kids != 2 || !r.expandable() {
		t.Fatalf("subagents only: %+v", r)
	}
	in.expanded = map[string]bool{paneNodeID(localMachine, "p1"): false}
	if got := render(buildTree(in)); strings.Contains(got, "/sub/") || !strings.Contains(got, "pane:p1\n") {
		t.Fatalf("folded:\n%s", got)
	}
	in.expanded = map[string]bool{}

	// An agent that has exited, or a pane with no agent, runs none.
	in.machines[0].panes[0].State = proto.PaneExited
	if got := render(buildTree(in)); strings.Contains(got, "/sub/") {
		t.Fatalf("exited agent:\n%s", got)
	}
	in.machines[0].panes[0].State = proto.PaneRunning
	in.machines[0].panes[0].Agent.Subagents = nil
	if got, plain := render(buildTree(in)), render(buildTree(sampleInput())); got != plain {
		t.Fatalf("no subagents changed the tree:\n%s\nwant\n%s", got, plain)
	}
}

// Grouped by tab, an agent's subagents are under it there too, and the
// tab's count is still of panes.
func TestBuildTreeSubagentsInATab(t *testing.T) {
	in := sampleInput()
	in.machines[0].panes[0].Agent.Subagents = []proto.Subagent{{ID: "a1"}, {ID: "a2"}}
	in.tabs = []treeTab{{machine: localMachine, projectID: "r1", label: "Tab 1  claude", n: 1, panes: []string{"p1"}}}
	rows := buildTree(in)
	got := render(rows)
	want := "        pane:p1\n          " + subagentID(localMachine, "p1", "a1") + "\n          " + subagentID(localMachine, "p1", "a2") + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("not under the agent in its tab:\n%s\nwant\n%s", got, want)
	}
	for _, r := range rows {
		if r.kind == kindTab && r.count != 1 {
			t.Fatalf("tab counts %d, want the one pane", r.count)
		}
	}
	in.expanded = map[string]bool{paneNodeID(localMachine, "p1"): false}
	if got := render(buildTree(in)); strings.Contains(got, "/sub/") {
		t.Fatalf("folded in a tab:\n%s", got)
	}
}

// a5Subagents gives p1 two subagents, one with a task and one known only
// by its type.
func a5Subagents(t *testing.T) *Model {
	t.Helper()
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[0].Agent.Subagents = []proto.Subagent{
		{ID: "a1", Type: "general-purpose", Description: "Writing gateway e2e tests in gateway.spec.ts"},
		{ID: "a2", Type: "Explore"},
	}
	m.rebuild()
	return m
}

func TestSubagentRowsSay(t *testing.T) {
	m := a5Subagents(t)
	parts := func(id string) (string, string) {
		t.Helper()
		i := indexOfRow(m.rows, subagentID(localMachine, "p1", id))
		if i < 0 {
			t.Fatalf("no row for %s in\n%s", id, render(m.rows))
		}
		_, _, label, _, right := m.rowParts(m.rows[i])
		return label, ansi.Strip(right)
	}
	if label, right := parts("a1"); label != "Writing gateway e2e tests in gateway.spec.ts" || right != "general-purpose" {
		t.Fatalf("with a task: %q %q", label, right)
	}
	if label, right := parts("a2"); label != "Explore" || right != "" {
		t.Fatalf("by type: %q %q", label, right)
	}
	agent := m.machines[0].panes[0].Agent
	agent.Subagents[1] = proto.Subagent{ID: "a2", Description: "only a task"}
	if label, right := parts("a2"); label != "only a task" || right != "" {
		t.Fatalf("task without a type: %q %q", label, right)
	}
	agent.Subagents[1] = proto.Subagent{ID: "a2"}
	if label, _ := parts("a2"); label != "subagent" {
		t.Fatalf("nothing known: %q", label)
	}
	// A row left from before an update that took the subagent away.
	gone := row{id: subagentID(localMachine, "p1", "zz"), kind: kindSubagent, machine: localMachine, paneID: "p1", label: "zz"}
	if _, _, label, _, _ := m.rowParts(gone); label != "subagent" {
		t.Fatalf("gone: %q", label)
	}
	gone.paneID = "nope"
	if _, _, label, _, _ := m.rowParts(gone); label != "zz" {
		t.Fatalf("agent gone: %q", label)
	}
}

// Rows never run past the sidebar, however narrow, with a long task.
func TestSubagentRowsFit(t *testing.T) {
	m := a5Subagents(t)
	m.machines[0].panes[0].Agent.Subagents[0].Description = strings.Repeat("a long task ", 30)
	for _, w := range []int{1, 2, 5, 12, 20, 39, 80, 300} {
		for _, r := range m.rows {
			if r.kind != kindSubagent {
				continue
			}
			if got := ansi.StringWidth(m.rowLine(r, w)); got > w {
				t.Fatalf("width %d: row %s is %d wide", w, r.id, got)
			}
		}
	}
	m.width, m.height = 20, 5
	m.rebuild()
	for _, l := range m.sidebarLines(10, 5) {
		if ansi.StringWidth(l) > 10 {
			t.Fatalf("sidebar line %q", l)
		}
	}
}

// A subagent has no screen: opening it opens the agent that runs it, with
// the cursor there, so keys go to that agent.
func TestEnterOnASubagentOpensItsAgent(t *testing.T) {
	m := a5Subagents(t)
	sub := subagentID(localMachine, "p1", "a1")
	m.cursor = sub
	m.syncView()
	// Browsing onto it previews its agent, as browsing onto the agent would.
	if v := m.tab().focused().view; v.Kind != kindPane || v.PaneID != "p1" || v.Row != paneNodeID(localMachine, "p1") {
		t.Fatalf("browsing onto a subagent shows %+v", v)
	}
	if m.cursor != sub {
		t.Fatalf("browsing moved the cursor to %s", m.cursor)
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := mm.(Model)
	if m2.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("cursor on %s", m2.cursor)
	}
	if v := m2.tab().focused().view; v.Kind != kindPane || v.PaneID != "p1" || m2.focus != focusMain {
		t.Fatalf("opened %+v, focus %v", v, m2.focus)
	}
}

// The same by mouse: a double click on the row.
func TestDoubleClickOnASubagentOpensItsAgent(t *testing.T) {
	m := a5Subagents(t)
	sub := subagentID(localMachine, "p1", "a2")
	y := a1RowY(t, m, sub)
	var cur tea.Model = *m
	for i := 0; i < 2; i++ {
		cur, _ = cur.Update(tea.MouseMsg{X: 12, Y: y, Button: a1Left, Action: a1Press})
		cur, _ = cur.Update(tea.MouseMsg{X: 12, Y: y, Button: a1Left, Action: a1Release})
	}
	got := cur.(Model)
	if v := got.tab().focused().view; v.Kind != kindPane || v.PaneID != "p1" {
		t.Fatalf("double click opened %+v (cursor %s)", v, got.cursor)
	}
}

// When a subagent's row is gone by the time it is opened, nothing happens.
func TestActivateASubagentWithoutItsAgent(t *testing.T) {
	m := a5Subagents(t)
	r := row{id: subagentID(localMachine, "gone", "a1"), kind: kindSubagent, machine: localMachine, paneID: "gone", label: "a1"}
	before := m.cursor
	if cmd := m.activate(r); cmd != nil || m.cursor != before {
		t.Fatalf("activated a subagent of no agent: cursor %s", m.cursor)
	}
}

// A subagent's menu holds nothing that would act on a pane it does not
// have — closing or renaming it would reach its agent.
func TestSubagentMenu(t *testing.T) {
	m := a5Subagents(t)
	r := m.rows[indexOfRow(m.rows, subagentID(localMachine, "p1", "a1"))]
	got := a2MenuLabels(newRowMenu(*m, r, 0, 0))
	for _, no := range []string{"Close", "Rename", "Prompt"} {
		if strings.Contains(got, no) {
			t.Fatalf("subagent menu offers %q:\n%s", no, got)
		}
	}
	if !strings.Contains(got, "enter Open its agent") {
		t.Fatalf("subagent menu: %s", got)
	}
	mu := newRowMenu(*m, r, 0, 0)
	m.cursor = r.id
	mu.items[0].run(m)
	if m.cursor != paneNodeID(localMachine, "p1") || m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("Open its agent: cursor %s, view %+v", m.cursor, m.tab().focused().view)
	}
}
