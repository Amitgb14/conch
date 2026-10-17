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
	// The other machine is told about none of its panes — it has none on
	// screen — and about the tab being looked at, with an empty list:
	// "the person is somewhere else" is the answer to the question, and
	// the keyboard is not its pane's.
	away := m.viewStateFor("busybox")
	if away.Focus != "" {
		t.Fatalf("busybox was given this computer's focus: %+v", away)
	}
	if len(away.Tabs) != 1 || !away.Tabs[0].Active || len(away.Tabs[0].Panes) != 0 {
		t.Fatalf("busybox: %+v", away.Tabs)
	}
	// The tab being looked at is there whatever is in it: a diff or a
	// project's page is still the answer to "what are you looking at".
	a1Open(t, m, projectNodeID(localMachine, "r1"))
	with := m.viewStateFor(localMachine)
	active := -1
	for i, tab := range with.Tabs {
		if tab.Active {
			active = i
		}
	}
	if active < 0 {
		t.Fatalf("nothing is marked active: %+v", with.Tabs)
	}
	if len(with.Tabs[active].Panes) != 0 {
		t.Fatalf("this case needs an active tab with no panes: %+v", with.Tabs[active])
	}
	// And a machine with nothing of its own still hears about it, since
	// the person is looking at something.
	if on := m.viewStateFor("busybox"); len(on.Tabs) != 1 || !on.Tabs[0].Active {
		t.Fatalf("busybox while the person is on a page: %+v", on)
	}
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	here = m.viewStateFor(localMachine)

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
