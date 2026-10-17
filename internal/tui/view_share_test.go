package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// What each machine is told is its own panes: a layout spans machines,
// but an agent asking its server what is on screen wants an answer in
// panes it could address.
func TestViewStateIsPerMachine(t *testing.T) {
	m, _ := a1Fixture(t, false)
	far := newMachine("busybox", "busybox", "me@box")
	far.state = stateOnline
	far.panes = []proto.PaneInfo{{ID: "q1", Name: "codex", State: proto.PaneRunning}}
	m.machines = append(m.machines, far)
	m.rebuild()

	a1Open(t, m, paneNodeID(localMachine, "p1"))
	here := m.viewStateFor(localMachine)
	if len(here.Tabs) == 0 || here.Focus != "p1" {
		t.Fatalf("this computer: %+v", here)
	}
	for _, tab := range here.Tabs {
		for _, id := range tab.Panes {
			if id == "q1" {
				t.Fatalf("another machine's pane was published here: %+v", here)
			}
		}
	}
	// The other machine has nothing on screen, and is told that rather
	// than being told about panes it does not own.
	if away := m.viewStateFor("busybox"); len(away.Tabs) != 0 || away.Focus != "" {
		t.Fatalf("busybox: %+v", away)
	}
	// The signature changes with the layout, which is what stops it
	// being published once a redraw.
	before := viewSig(here)
	if viewSig(m.viewStateFor(localMachine)) != before {
		t.Fatal("the same layout gave two signatures")
	}
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	if viewSig(m.viewStateFor(localMachine)) == before {
		t.Fatal("opening another pane left the signature alone")
	}
}

// A show request opens a tab of its own rather than rearranging the one
// somebody is typing in, and says who asked.
func TestShowPanesOpensItsOwnTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))

	// One pane is a reveal — the tree lands on it and conch shows it,
	// exactly as ! does for a waiting agent — and nothing is arranged:
	// no split is made for a single pane.
	m.showPanes(localMachine, proto.ViewShowParams{Panes: []string{"p3"}, Why: "the agent in p1"})
	if m.cursor != paneNodeID(localMachine, "p3") || !strings.Contains(m.flash, "the agent in p1") {
		t.Fatalf("cursor %q flash %q", m.cursor, m.flash)
	}
	for _, tab := range m.tabs {
		if len(tab.root.leaves()) > 1 {
			t.Fatalf("revealing one pane split a tab: %d leaves", len(tab.root.leaves()))
		}
	}
	tabs := len(m.tabs)

	// Two panes end up side by side in one tab, named as asked. They are
	// gathered rather than copied: a pane is shown in exactly one place
	// (tabs.go), so showing p3 beside p4 moves them, and conch's own
	// break-pane rule reuses a tab that already holds one of them alone
	// rather than leaving an empty one behind.
	_ = tabs
	m.showPanes(localMachine, proto.ViewShowParams{Panes: []string{"p3", "p4"}, Name: "those two", Why: "claude"})
	tab := m.tab()
	if tab.name != "those two" {
		t.Fatalf("tab name %q", tab.name)
	}
	var in []string
	for _, l := range tab.root.leaves() {
		in = append(in, l.view.PaneID)
	}
	if strings.Join(in, ",") != "p3,p4" {
		t.Fatalf("the tab holds %v", in)
	}
	// And each of them is in that one place only.
	for i, other := range m.tabs {
		if other == tab {
			continue
		}
		for _, l := range other.root.leaves() {
			if l.view.PaneID == "p3" || l.view.PaneID == "p4" {
				t.Fatalf("tab %d still shows %s", i, l.view.PaneID)
			}
		}
	}
	if !strings.Contains(m.flash, "claude") || !strings.Contains(m.flash, "2 panes") {
		t.Fatalf("flash %q", m.flash)
	}

	// Panes that have gone say so rather than opening an empty tab.
	tabs = len(m.tabs)
	m.showPanes(localMachine, proto.ViewShowParams{Panes: []string{"gone"}})
	if len(m.tabs) != tabs || !strings.Contains(m.flash, "not here any more") {
		t.Fatalf("a pane that went: %d tabs, %q", len(m.tabs), m.flash)
	}
}
