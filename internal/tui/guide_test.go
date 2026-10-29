package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// guidePage is the local machine's page as text.
func guidePage(m *Model, cols int) string {
	return ansi.Strip(strings.Join(m.machineLines(m.machines[0], cols, 40), "\n"))
}

// A newcomer's first screen says what conch is for, a step at a time, and
// each step ticks itself off as it is done.
func TestGuideSteps(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.projects, mach.panes = nil, nil

	page := guidePage(m, 120)
	for _, want := range []string{"Getting started  1 of 5 · x hides this", "✓ c  install an agent", "a  add a project", "t  on a project, give an agent a task",
		"Q  the review queue", "q  detach whenever you like"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q:\n%s", want, page)
		}
	}
	// The guide says what a and t are for, so the hints under it don't
	// say them again; c is said once.
	if strings.Contains(page, "a  add a project      t") || strings.Count(page, "c  start") != 1 {
		t.Fatalf("said twice:\n%s", page)
	}

	// A project anywhere is the first step, read off the machines.
	mach.projects = []proto.ProjectInfo{{ID: "r1", Name: "api"}}
	if page := guidePage(m, 120); !strings.Contains(page, "2 of 5") || !strings.Contains(page, "✓ a  add a project") {
		t.Fatalf("a project:\n%s", page)
	}

	// The others are noted as they happen.
	m.guideNote(guideTask)
	m.guideNote(guideQueue)
	if page := guidePage(m, 120); !strings.Contains(page, "4 of 5") {
		t.Fatalf("three done:\n%s", page)
	}
	// All five: gone, and the hints are whole again.
	m.guideNote(guideReturned)
	page = guidePage(m, 120)
	if strings.Contains(page, "Getting started") || !strings.Contains(page, "a  add a project      t  new task in its home") {
		t.Fatalf("all done:\n%s", page)
	}
	if m.guideShown() || m.guideLines() != nil {
		t.Fatal("shown with every step done")
	}
}

// Only this computer's page has it: the steps are about using conch, not
// about any one machine, and repeating them on every machine is noise.
func TestGuideOnlyOnTheLocalPage(t *testing.T) {
	m, _ := a1Fixture(t, false)
	remote := newMachine("box", "box", "me@box")
	remote.state = stateOnline
	m.machines = append(m.machines, remote)
	if page := ansi.Strip(strings.Join(m.machineLines(remote, 120, 40), "\n")); strings.Contains(page, "Getting started") {
		t.Fatalf("on a remote machine's page:\n%s", page)
	}
	if !strings.Contains(guidePage(m, 120), "Getting started") {
		t.Fatal("not on the local page")
	}
}

// x hides it for good, and a hidden guide stays hidden whatever is left.
func TestGuideHidden(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.guide = map[string]bool{guideHidden: true}
	if m.guideShown() || strings.Contains(guidePage(m, 120), "Getting started") {
		t.Fatal("hidden, yet shown")
	}
}

// Every line stays inside the page, however narrow, and a short page
// does not panic.
func TestGuideFits(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {39, 20}, {20, 5}, {1, 1}} {
		for i, l := range m.machineLines(m.machines[0], size[0], size[1]) {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, ansi.Strip(l))
			}
		}
	}
}

// What the panes show: an agent in a project is a task given; an agent
// in the first list this TUI gets is one that outlived the TUI before
// it. A shell is neither, and a later list proves nothing about quitting.
func TestGuideAgents(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]

	// A shell, and an agent outside every project: no task given.
	m.guide = nil
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "zsh", ProjectID: "r1"},
		{ID: "p2", Name: "codex", Agent: &proto.AgentStatus{Name: "codex"}},
	}
	m.guideAgents(mach, false)
	if m.guide[guideTask] || m.guide[guideReturned] {
		t.Fatalf("noted %v", m.guide)
	}
	// The first list, with an agent in it, is coming back to one.
	m.guideAgents(mach, true)
	if !m.guide[guideReturned] || m.guide[guideTask] {
		t.Fatalf("first list: %v", m.guide)
	}
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "p3", ProjectID: "r1", Agent: &proto.AgentStatus{Name: "claude"}})
	m.guideAgents(mach, false)
	if !m.guide[guideTask] {
		t.Fatalf("an agent in a project: %v", m.guide)
	}

	// Through the messages themselves: only the local machine's first
	// list counts as coming back.
	m.guide, m.guideSawLocal = nil, false
	agent := []proto.PaneInfo{{ID: "p9", Name: "claude", Agent: &proto.AgentStatus{Name: "claude"}}}
	updateModel(m, panesMsg{machine: localMachine, gen: mach.gen, panes: agent})
	if !m.guide[guideReturned] {
		t.Fatalf("an agent in the first list: %v", m.guide)
	}
	m.guide = nil
	updateModel(m, panesMsg{machine: localMachine, gen: mach.gen, panes: agent})
	if m.guide[guideReturned] {
		t.Fatal("a later list counted as coming back")
	}

	// An agent appearing in a project, as an event.
	m.guide = nil
	mach.panes = []proto.PaneInfo{{ID: "p9", Name: "claude"}}
	m.handleEvent(mach, eventMsg(t, proto.EventPaneUpdated,
		proto.PaneInfo{ID: "p9", Name: "claude", ProjectID: "r1", Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}}))
	if !m.guide[guideTask] {
		t.Fatalf("the event: %v", m.guide)
	}
}

// Opening the queue, by key or however else, is its step.
func TestGuideQueue(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.guide = nil
	m.show(queueRow())
	if !m.guide[guideQueue] {
		t.Fatalf("queue: %v", m.guide)
	}
	// Anything else shown is not.
	m.guide = nil
	m.show(row{id: machineID(localMachine), kind: kindMachine, machine: localMachine})
	if m.guide[guideQueue] {
		t.Fatal("another row counted as the queue")
	}
}

// The steps live in ui.json: kept across restarts, absent from an older
// file (which then loads with the guide up), and shared with another TUI
// a step at a time.
func TestGuideSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	os.WriteFile(path, []byte(`{"expanded":{},"show_all":{}}`), 0o600)
	if st := loadUIState(path); st.Guide != nil {
		t.Fatalf("an older file: %v", st.Guide)
	}

	st := uiState{Guide: map[string]bool{guideTask: true, guideHidden: true, "stale": false}}
	if err := saveUIState(path, st); err != nil {
		t.Fatal(err)
	}
	if back := loadUIState(path); !back.Guide[guideTask] || !back.Guide[guideHidden] {
		t.Fatalf("loaded %v", back.Guide)
	}

	s := sharedOf(st)
	if len(s) != 2 { // a step not done is nothing to share
		t.Fatalf("pieces %v", s)
	}
	var back uiState
	back.Guide = map[string]bool{"old": true}
	s.into(&back)
	if len(back.Guide) != 2 || !back.Guide[guideTask] || !back.Guide[guideHidden] {
		t.Fatalf("into %v", back.Guide)
	}
	// A step done in another TUI comes in; one done here stays.
	base := sharedOf(uiState{})
	ours := sharedOf(uiState{Guide: map[string]bool{guideTask: true}})
	theirs := sharedOf(uiState{Guide: map[string]bool{guideQueue: true}})
	var merged uiState
	mergeShared(base, ours, theirs).into(&merged)
	if !merged.Guide[guideTask] || !merged.Guide[guideQueue] {
		t.Fatalf("merged %v", merged.Guide)
	}
	b, _ := json.Marshal(uiState{})
	if strings.Contains(string(b), "guide") {
		t.Fatalf("an empty guide is written: %s", b)
	}
}

// With no agent installed the guide starts there, and c says it once:
// in the guide, not again under the agent list.
func TestGuideInstallAnAgent(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.agentList = []proto.AgentAvailability{{Name: "claude"}, {Name: "codex"}}
	page := guidePage(m, 120)
	if !strings.Contains(page, "1 of 5") || !strings.Contains(page, "  c  install an agent: Claude Code, Codex") {
		t.Fatalf("none installed:\n%s", page)
	}
	if strings.Contains(page, "c  start or install an agent") {
		t.Fatalf("c said twice:\n%s", page)
	}

	// One installed: ticked, and the hint under the list is back.
	mach.agentList[1].Installed = true
	page = guidePage(m, 120)
	if !strings.Contains(page, "✓ c  install an agent") || !strings.Contains(page, "c  start or install an agent") {
		t.Fatalf("one installed:\n%s", page)
	}

	// Hidden guide, nothing installed: the hint is the only place c is
	// said, so it stays.
	mach.agentList[1].Installed = false
	m.guideNote(guideHidden)
	if page := guidePage(m, 120); !strings.Contains(page, "c  start or install an agent") {
		t.Fatalf("hidden guide:\n%s", page)
	}

	// Only this computer's list counts: a remote's agents are not this one's.
	m.guide = nil
	remote := newMachine("box", "box", "me@box")
	remote.agentList = []proto.AgentAvailability{{Name: "claude", Installed: true}}
	m.machines = append(m.machines, remote)
	if m.agentInstalled() {
		t.Fatal("a remote machine's agent counted as installed here")
	}
}

// The agent c and t start: Settings' choice, else Claude Code, else one
// that is installed — not an install prompt for Claude Code when Codex
// is right there.
func TestDefaultAgentIsOneInstalled(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	for _, tc := range []struct {
		name    string
		setting string
		list    []proto.AgentAvailability
		want    string
	}{
		{"no list yet", "", nil, "claude"},
		{"claude installed", "", []proto.AgentAvailability{{Name: "claude", Installed: true}, {Name: "codex", Installed: true}}, "claude"},
		{"only codex", "", []proto.AgentAvailability{{Name: "claude"}, {Name: "codex", Installed: true}}, "codex"},
		{"supported before runs-here", "", []proto.AgentAvailability{{Name: "claude"}, {Name: "aider", Installed: true, Tier: proto.TierRunsHere}, {Name: "gemini", Installed: true, Tier: proto.TierSupported}}, "gemini"},
		{"only one of somebody's own", "", []proto.AgentAvailability{{Name: "claude"}, {Name: "aider", Installed: true, Tier: proto.TierRunsHere}}, "aider"},
		{"none installed", "", []proto.AgentAvailability{{Name: "claude"}, {Name: "codex"}}, "claude"},
		{"Settings wins", "gemini", []proto.AgentAvailability{{Name: "claude"}, {Name: "codex", Installed: true}}, "gemini"},
	} {
		m.cfg.Agents.Default = tc.setting
		mach.agentList, mach.available = tc.list, nil
		if tc.list != nil {
			mach.available = map[string]proto.AgentAvailability{}
			for _, a := range tc.list {
				mach.available[a.Name] = a
			}
		}
		if got := m.defaultAgent(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
