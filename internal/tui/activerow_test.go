package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// clickTab clicks the tab bar entry of the tab showing pane id.
func clickTab(t *testing.T, m *Model, id string) {
	t.Helper()
	mr := m.mainRect()
	_, hits := m.tabBar(mr.w)
	for _, h := range hits {
		if h.tab >= 0 && m.tabs[h.tab].root.leaves()[0].view.PaneID == id {
			a2Run(a1Mouse(t, m, mr.x+h.x0, mr.y, a1Left, a1Press))
			return
		}
	}
	t.Fatalf("no tab for %s in %+v", id, hits)
}

// Clicking a pane's tab leaves the tree's cursor where it was; the status
// bar used to follow the cursor and said CHANGES while typing in a terminal.
func TestActiveRowStatusFollowsClickedTab(t *testing.T) {
	for _, cursor := range []string{
		looseTerminalsID(localMachine), // the manual steps: Terminals, then the tab
		cliID(localMachine),
	} {
		t.Run(cursor, func(t *testing.T) {
			m, _ := a1Fixture(t, true)
			a1Open(t, m, paneNodeID(localMachine, "p3"))
			a1At(t, m, cursor)
			if m.focus != focusSidebar || a1Chip(m) != "TREE" {
				t.Fatalf("on %s: focus %v chip %s", cursor, m.focus, a1Chip(m))
			}
			clickTab(t, m, "p3")
			if m.focus != focusMain || m.cursor != cursor {
				t.Fatalf("click: focus %v cursor %s", m.focus, m.cursor)
			}
			if got := a1HintText(m); a1Chip(m) != "PANE" || !strings.HasPrefix(got, "ctrl+b tree|ctrl+b v split") {
				t.Fatalf("chip %s hints %s", a1Chip(m), got)
			}

			// The other pane chips follow it too.
			m.offset = 3
			if a1Chip(m) != "HISTORY" {
				t.Errorf("history: %s", a1Chip(m))
			}
			m.offset, m.scrollMode = 0, true
			if a1Chip(m) != "SCROLL" {
				t.Errorf("scroll: %s", a1Chip(m))
			}
			m.scrollMode = false
			m.tab().sync = true
			if a1Chip(m) != "SYNC" {
				t.Errorf("sync: %s", a1Chip(m))
			}
			m.tab().sync = false

			// Its hints act on the pane: ctrl+b tree returns to the tree.
			_, items := m.statusHints()
			for _, it := range items {
				if ansi.Strip(it.text) != "" && strings.HasPrefix(strings.TrimSpace(ansi.Strip(it.text)), "ctrl+b tree") && it.act != nil {
					it.act(m)
				}
			}
			if m.focus != focusSidebar || a1Chip(m) != "TREE" {
				t.Fatalf("back to tree: focus %v chip %s", m.focus, a1Chip(m))
			}
		})
	}
}

func TestActiveRow(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	a1At(t, m, cliID(localMachine))

	// In the tree: the cursor.
	if r := m.activeRow(); r.kind != kindCLI || r.id != cliID(localMachine) {
		t.Fatalf("tree focus: %+v", r)
	}
	// In the main area: what the focused split shows.
	m.activeTab, m.previewing = 0, false
	m.focus = focusMain
	if r := m.activeRow(); r.kind != kindPane || r.paneID != "p3" || r.machine != localMachine {
		t.Fatalf("main focus: %+v", r)
	}
	// An empty split shows nothing: the cursor again.
	m.tab().focused().view = viewRef{}
	if r := m.activeRow(); r.kind != kindCLI {
		t.Fatalf("empty split: %+v", r)
	}
	// No cursor and nothing shown: the zero row.
	m.cursor = ""
	if r := m.activeRow(); r.id != "" {
		t.Fatalf("nothing: %+v", r)
	}
}

// Typing and the status bar agree on the pane after clicking its tab.
func TestActiveRowTypingMatchesStatus(t *testing.T) {
	m, peer := a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	a1At(t, m, looseTerminalsID(localMachine))
	clickTab(t, m, "p3")
	a1Key(t, m, runes("h"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	heldWait(t, peer, "p3", "h|<enter>")
	if a1Chip(m) != "PANE" {
		t.Fatalf("chip %s", a1Chip(m))
	}
}

// Plan limits in the status bar name the agent being typed into, even with
// the cursor elsewhere.
func TestActiveRowLimits(t *testing.T) {
	m, _ := a1Fixture(t, true)
	now := time.Now()
	mach := m.machines[0]
	mach.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 10}})
	mach.setLimits(proto.PlanLimits{Agent: "codex", Week: &proto.LimitWindow{UsedPct: 50}})

	a1Open(t, m, paneNodeID(localMachine, "p4")) // codex
	a1At(t, m, cliID(localMachine))
	if got := ansi.Strip(m.statusLimits(now)[0].text); !strings.HasPrefix(got, "Claude") {
		t.Fatalf("cursor on CLI: %q", got)
	}
	clickTab(t, m, "p4")
	if got := ansi.Strip(m.statusLimits(now)[0].text); got != "Codex 7d 50%" {
		t.Fatalf("typing into codex: %q", got)
	}

	// A remote agent's split shows that machine's limits, not local's.
	box := newMachine("box", "box", "dev@box")
	box.state = stateOnline
	box.panes = []proto.PaneInfo{{ID: "q1", Name: "gemini", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "gemini"}}}
	box.setLimits(proto.PlanLimits{Agent: "gemini", Week: &proto.LimitWindow{UsedPct: 7}})
	m.machines = append(m.machines, box)
	m.rebuild()
	m.tab().focused().view = viewRef{Row: paneNodeID("box", "q1"), Kind: kindPane, Machine: "box", PaneID: "q1"}
	if got := ansi.Strip(m.statusLimits(now)[0].text); !strings.HasPrefix(got, "Gemini") || !strings.HasSuffix(got, "7d 7%") {
		t.Fatalf("remote agent: %q", got)
	}
}
