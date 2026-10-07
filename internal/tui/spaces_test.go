package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// headerX is the screen column of the header button for space sp (or
// spaceHitPlus / spaceHitClose), or -1.
func headerX(m *Model, sp int) int {
	sw, _ := m.sidebarInner()
	_, hits := m.spaceHeader(max(sw-2, 0))
	for _, h := range hits {
		if h.space == sp {
			return h.x0 + 2
		}
	}
	return -1
}

func topLine(m *Model) string {
	return ansi.Strip(strings.SplitN(m.View(), "\n", 2)[0])
}

func clickTop(t *testing.T, m *Model, sp int) tea.Cmd {
	t.Helper()
	x := headerX(m, sp)
	if x < 0 {
		t.Fatalf("no header button %d in %q", sp, topLine(m))
	}
	return a1Mouse(t, m, x, 0, tea.MouseButtonLeft, tea.MouseActionPress)
}

func TestSpaceHeader(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if got := topLine(m); !strings.HasPrefix(got, "╭─ ◆ conch  + ─") {
		t.Fatalf("one workspace: %q", got)
	}
	if x := headerX(m, 0); x >= 0 {
		t.Fatalf("a lone workspace has no number to click: %d", x)
	}

	m.newSpace()
	m.newSpace()
	if got := topLine(m); !strings.Contains(got, "◆ conch  1  2  3 ×  + ") {
		t.Fatalf("three workspaces: %q", got)
	}
	m.spaces[1].name = "review"
	m.sidebarW = 40
	if got := topLine(m); !strings.Contains(got, "conch  1  2 review  3 ×  + ") {
		t.Fatalf("named: %q", got)
	}
	// Too many to name: numbers. Too many to number: the one on screen
	// is kept, with those after it.
	m.spaces[2].name = "a long name that cannot fit"
	if got := topLine(m); !strings.Contains(got, "conch  1  2  3 ×  + ") {
		t.Fatalf("names that don't fit: %q", got)
	}
	for range 6 {
		m.newSpace()
	}
	m.switchSpace(7)
	if got := topLine(m); !strings.Contains(got, " 8 ×") || !strings.Contains(got, " + ") || strings.Contains(got, " 1 ") {
		t.Fatalf("many: %q", got)
	}

	// Narrow sidebars and screens: the border never grows past the width,
	// workspaces that don't fit are left out, and the + stays while it fits.
	for _, size := range [][3]int{{160, 40, 8}, {160, 40, 14}, {160, 40, 16}, {160, 40, 20}, {20, 5, 12}, {1, 1, 1}, {39, 10, 30}} {
		m.width, m.height, m.sidebarW = size[0], size[1], size[2]
		for i, l := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(l); w > m.width {
				t.Fatalf("%v line %d is %d wide: %q", size, i, w, ansi.Strip(l))
			}
		}
		sw, _ := m.sidebarInner()
		header, hits := m.spaceHeader(max(sw-2, 0))
		if w := ansi.StringWidth(header); w > max(sw-2, 0) && len(hits) > 0 {
			t.Fatalf("%v header %d wide in %d: %q", size, w, sw-2, ansi.Strip(header))
		}
		if sw-2 >= 13 && headerX(m, spaceHitPlus) < 0 {
			t.Fatalf("%v: the + went: %q", size, ansi.Strip(header))
		}
	}
}

func TestSpacePlusIsolates(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	first := m.tab()
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}

	clickTop(t, m, spaceHitPlus)
	if m.activeSpace != 1 || len(m.spaces) != 2 {
		t.Fatalf("space %d of %d", m.activeSpace, len(m.spaces))
	}
	// Its own tree: the machine and nothing of the first workspace's.
	if len(m.rows) != 1 || m.rows[0].kind != kindMachine {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	if len(m.tabs) != 0 {
		t.Fatalf("tabs carried over: %d", len(m.tabs))
	}
	side := ansi.Strip(m.View())
	if !strings.Contains(side, "a  add a project") {
		t.Fatalf("no hints in an empty workspace:\n%s", side)
	}

	// Back to the first: its tree and the tab it was on.
	clickTop(t, m, 0)
	if m.activeSpace != 0 || m.tab() != first || indexOfRow(m.rows, projectNodeID(localMachine, "r1")) < 0 {
		t.Fatalf("first workspace not restored: space %d\n%s", m.activeSpace, render(m.rows))
	}
	// And to the second again with the keys.
	a1Prefixed(t, m, runes(")"))
	if m.activeSpace != 1 || len(m.tabs) != 0 {
		t.Fatalf(") went to %d", m.activeSpace)
	}
	a1Prefixed(t, m, runes(")"))
	if m.activeSpace != 0 {
		t.Fatalf(") round the end went to %d", m.activeSpace)
	}
	a1Prefixed(t, m, runes("("))
	if m.activeSpace != 1 {
		t.Fatalf("( round the start went to %d", m.activeSpace)
	}
}

func TestSpaceStepWithOne(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if m.stepSpace(1) != nil || m.activeSpace != 0 || !strings.Contains(m.flash, "+") {
		t.Fatalf("one workspace: space %d flash %q", m.activeSpace, m.flash)
	}
	if m.switchSpace(5) != nil || m.switchSpace(-1) != nil {
		t.Fatal("switched to a workspace that isn't there")
	}
}

func TestSpaceFilter(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Prefixed(t, m, runes("N"))
	if m.activeSpace != 1 {
		t.Fatalf("ctrl+b N: space %d", m.activeSpace)
	}

	// A project added from here joins it, with what runs in it.
	info := proto.ProjectInfo{ID: "r1", Name: "api"}
	msg := m.addedProject(localMachine, &info, "added ")().(spaceProjectMsg)
	next, _ := m.Update(msg)
	*m = next.(Model)
	if indexOfRow(m.rows, projectNodeID(localMachine, "r1")) < 0 || indexOfRow(m.rows, paneNodeID(localMachine, "p1")) < 0 {
		t.Fatalf("project not shown:\n%s", render(m.rows))
	}
	if indexOfRow(m.rows, paneNodeID(localMachine, "p3")) >= 0 || indexOfRow(m.rows, cliID(localMachine)) >= 0 {
		t.Fatalf("the first workspace's CLI panes show:\n%s", render(m.rows))
	}
	if m.flash != "added api" {
		t.Fatalf("flash %q", m.flash)
	}

	// A terminal started here outside any project joins it, and what an
	// agent here starts follows its agent.
	mach := m.machines[0]
	next, _ = m.Update(createdMsg{machine: localMachine, info: proto.PaneInfo{ID: "p5", Name: "sh", State: proto.PaneRunning}})
	*m = next.(Model)
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "p6", Name: "helper", State: proto.PaneRunning, CreatedBy: "p5"},
		proto.PaneInfo{ID: "p7", Name: "elsewhere", State: proto.PaneRunning})
	m.rebuild()
	for id, want := range map[string]bool{"p5": true, "p6": true, "p7": false, "p3": false} {
		if got := indexOfRow(m.rows, paneNodeID(localMachine, id)) >= 0; got != want {
			t.Fatalf("pane %s shown %v:\n%s", id, got, render(m.rows))
		}
	}

	// The first workspace still has everything.
	m.switchSpace(0)
	for _, id := range []string{"p3", "p5", "p6", "p7"} {
		if indexOfRow(m.rows, paneNodeID(localMachine, id)) < 0 {
			t.Fatalf("first workspace lacks %s:\n%s", id, render(m.rows))
		}
	}

	// A project added in the first workspace joins nothing.
	msg = m.addedProject(localMachine, &proto.ProjectInfo{ID: "r9", Name: "x"}, "added ")().(spaceProjectMsg)
	if msg.space != nil {
		t.Fatal("first workspace gave a space")
	}
	m.receiveSpaceProject(msg)
	if m.spaces[1].projects[scoped(localMachine, "r9")] {
		t.Fatal("joined a workspace not on screen")
	}
	// A reply for a workspace closed since is only a flash.
	gone := newSpace()
	m.receiveSpaceProject(spaceProjectMsg{space: gone, machine: localMachine, info: proto.ProjectInfo{ID: "r1"}, flash: "added api"})
	if len(gone.projects) != 0 {
		t.Fatal("closed workspace changed")
	}
}

func TestSpaceRemoveProjectOnlyLeaves(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.newSpace()
	m.spaces[1].projects[scoped(localMachine, "r1")] = true
	m.rebuild()
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.openRemove()
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "out of this workspace") {
		t.Fatalf("overlay %#v", m.overlay)
	}
	m.overlay = nil
	if cmd := d.submit(m, nil); cmd != nil {
		cmd()
	}
	if indexOfRow(m.rows, projectNodeID(localMachine, "r1")) >= 0 || m.project(localMachine, "r1") == nil {
		t.Fatalf("not taken out, or removed from the machine:\n%s", render(m.rows))
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	for _, msg := range peer.msgs {
		if msg.Method == proto.MethodProjectRemove {
			t.Fatal("asked the server to remove the project")
		}
	}
}

func TestSpaceCloseAndRename(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Prefixed(t, m, runes("X"))
	if m.overlay != nil || !strings.Contains(m.flash, "first workspace stays") {
		t.Fatalf("closing the first: overlay %v flash %q", m.overlay, m.flash)
	}
	m.newSpace()
	m.newSpace()
	m.switchSpace(1)
	a1Prefixed(t, m, runes("$"))
	d := m.overlay.(*dialog)
	m.overlay = nil
	d.submit(m, []string{"  review "})
	if m.spaces[1].name != "review" {
		t.Fatalf("name %q", m.spaces[1].name)
	}

	// × beside the workspace on screen asks, then closes only it.
	clickTop(t, m, spaceHitClose)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "keep running") {
		t.Fatalf("overlay %#v", m.overlay)
	}
	m.overlay = nil
	d.submit(m, nil)
	if len(m.spaces) != 2 || m.activeSpace != 0 || m.spaces[1].name != "" {
		t.Fatalf("after close: %d spaces, on %d", len(m.spaces), m.activeSpace)
	}
	// Closing the last extra one leaves just the one, as before any.
	m.switchSpace(1)
	m.closeSpace(1)
	if m.spaces != nil || m.activeSpace != 0 {
		t.Fatalf("after last close: %v on %d", m.spaces, m.activeSpace)
	}
	if got := topLine(m); !strings.HasPrefix(got, "╭─ ◆ conch  + ─") {
		t.Fatalf("header %q", got)
	}
	// Closing one that isn't on screen keeps the one that is.
	m.newSpace()
	m.newSpace()
	m.closeSpace(1)
	if m.activeSpace != 1 || len(m.spaces) != 2 {
		t.Fatalf("on %d of %d", m.activeSpace, len(m.spaces))
	}
	if m.closeSpace(0) != nil || m.closeSpace(7) != nil {
		t.Fatal("closed the first or one that isn't there")
	}
}

func TestSpaceDropsPanesThatEndedAway(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	a1Open(t, m, paneNodeID(localMachine, "p4"))
	if len(m.tabs) != 2 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	m.newSpace()
	mach := m.machines[0]
	mach.panes = mach.panes[:3] // p4 ended while the first workspace was away
	m.switchSpace(0)
	if len(m.tabs) != 1 || m.tab().focused().view.PaneID != "p3" {
		t.Fatalf("tabs %d, focused %+v", len(m.tabs), m.tab().focused().view)
	}
	// An offline machine says nothing about its panes: they stay.
	m.newSpace()
	mach.state, mach.panes = stateOffline, nil
	m.switchSpace(0)
	if len(m.tabs) != 1 {
		t.Fatalf("offline machine's tab dropped: %d", len(m.tabs))
	}
}

func TestSpaceStateRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONCH_HOME", t.TempDir())
	m, _ := a1Fixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	m.newSpace()
	s := m.spaces[1]
	s.name = "review"
	s.projects[scoped(localMachine, "r1")] = true
	s.panes[paneKey(localMachine, "p4")] = true
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p4"))
	m.saveState()()

	st := loadUIState(m.statePath)
	// The first workspace's tabs stay where an older build reads them.
	if len(st.Tabs) != 1 || len(st.Spaces) != 1 || st.ActiveSpace != 1 {
		t.Fatalf("saved %d tabs, %d spaces, active %d", len(st.Tabs), len(st.Spaces), st.ActiveSpace)
	}
	ss := st.Spaces[0]
	if ss.Name != "review" || len(ss.Projects) != 1 || len(ss.Panes) != 1 || len(ss.Tabs) != 1 {
		t.Fatalf("saved space %+v", ss)
	}

	m2, _ := a1Fixture(t, false)
	m2.preview, m2.previewing = nil, false // as New has it: tabs come before any tree
	m2.restoreTabs(st.Tabs, st.ActiveTab)
	m2.restoreSpaces(st.Spaces, st.ActiveSpace)
	m2.rebuild()
	if m2.activeSpace != 1 || len(m2.spaces) != 2 || m2.spaces[1].name != "review" {
		t.Fatalf("restored on %d of %d", m2.activeSpace, len(m2.spaces))
	}
	if len(m2.tabs) != 1 || m2.tab().focused().view.PaneID != "p4" {
		t.Fatalf("restored tabs %d", len(m2.tabs))
	}
	if indexOfRow(m2.rows, paneNodeID(localMachine, "p4")) < 0 || indexOfRow(m2.rows, paneNodeID(localMachine, "p3")) >= 0 {
		t.Fatalf("restored tree:\n%s", render(m2.rows))
	}
	m2.switchSpace(0)
	if len(m2.tabs) != 1 || m2.tab().focused().view.PaneID != "p3" {
		t.Fatalf("first workspace's tabs %d: %+v", len(m2.tabs), m2.tab().focused().view)
	}

	// Saved with the first on screen, and a file from before workspaces.
	m2.statePath = m.statePath
	m2.saveState()()
	if st := loadUIState(m.statePath); st.ActiveSpace != 0 || len(st.Tabs) != 1 || len(st.Spaces) != 1 {
		t.Fatalf("second save: %d tabs, %d spaces, active %d", len(st.Tabs), len(st.Spaces), st.ActiveSpace)
	}
	m3, _ := a1Fixture(t, false)
	m3.restoreSpaces(nil, 3)
	if m3.spaces != nil || m3.activeSpace != 0 {
		t.Fatal("no saved spaces made some")
	}
	m3.restoreSpaces([]savedSpace{{}}, 9) // an active one that isn't there
	if len(m3.spaces) != 2 || m3.activeSpace != 0 {
		t.Fatalf("out of range active: %d of %d", m3.activeSpace, len(m3.spaces))
	}
}

func TestSpaceMachineCounts(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.panes[0].Agent.State = proto.AgentBlocked // p1, in api
	mach.panes[0].Agent.Tokens = &proto.Tokens{Input: 1000}
	mach.panes[3].Agent.Tokens = &proto.Tokens{Input: 5000} // p4, codex outside every project
	page := func() string { return ansi.Strip(strings.Join(m.machineLines(mach, 120, 30), "\n")) }
	if got := page(); !strings.Contains(got, "1 projects · 4 panes · 1 working · 1 waiting") {
		t.Fatalf("first workspace:\n%s", got)
	}
	all := m.machineUsage(localMachine)

	m.newSpace()
	if got := page(); !strings.Contains(got, "0 projects · 0 panes · 0 working · 0 waiting") {
		t.Fatalf("empty workspace:\n%s", got)
	}
	if b := m.attentionBadge(localMachine, ""); b != "" {
		t.Fatalf("empty workspace badge %q", ansi.Strip(b))
	}
	if u := m.machineUsage(localMachine); !u.empty() {
		t.Fatalf("empty workspace usage %+v", u)
	}
	if got := ansi.Strip(strings.Join(m.workspaceLines(mach, 90), "\n")); !strings.Contains(got, "0 projects on local") || !strings.Contains(got, "no projects yet") {
		t.Fatalf("workspace page:\n%s", got)
	}

	m.spaces[1].projects[scoped(localMachine, "r1")] = true
	m.rebuild()
	if got := page(); !strings.Contains(got, "1 projects · 2 panes · 0 working · 1 waiting") {
		t.Fatalf("with api:\n%s", got)
	}
	if b := ansi.Strip(m.attentionBadge(localMachine, "")); b != "⚑1" {
		t.Fatalf("badge %q", b)
	}
	if u := m.machineUsage(localMachine); u.empty() || u == all {
		t.Fatalf("usage %+v, everything %+v", u, all)
	}
	if got := ansi.Strip(strings.Join(m.workspaceLines(mach, 90), "\n")); !strings.Contains(got, "1 projects on local") || !strings.Contains(got, "1 agents · 1 terminals") {
		t.Fatalf("workspace page:\n%s", got)
	}

	// Offline, the panes last seen are this workspace's too.
	mach.state = stateOffline
	if got := page(); !strings.Contains(got, "showing 2 panes as last seen") {
		t.Fatalf("offline:\n%s", got)
	}
	// Back in the first, everything counts again.
	mach.state = stateOnline
	m.switchSpace(0)
	if got := page(); !strings.Contains(got, "4 panes") || m.machineUsage(localMachine) != all {
		t.Fatalf("first workspace again:\n%s", got)
	}
}

func TestSpaceFolders(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	p1, p3 := mach.panes[0], mach.panes[2]
	agents := func(name string) string { return folderRowID(localMachine, "r1", kindAgents, name) }
	ops := folderRowID(localMachine, "", kindTerminals, "ops")
	shows := func(id string) bool { return indexOfRow(m.rows, id) >= 0 }

	// In the first workspace: an empty folder, one holding the project's
	// agent, one holding a CLI terminal, and an SSH folder with a host.
	m.newFolder(localMachine, "r1", kindAgents, "later")
	m.newFolder(localMachine, "r1", kindAgents, "team")
	m.putInFolder(localMachine, "r1", kindAgents, "team", p1)
	m.newFolder(localMachine, "", kindTerminals, "ops")
	m.putInFolder(localMachine, "", kindTerminals, "ops", p3)
	m.folders[sshFolderKey()] = []savedFolder{{Name: "hosts", Members: []savedMember{{Host: "box"}}}}
	m.rebuild()
	for _, id := range []string{agents("later"), agents("team"), ops} {
		if !shows(id) {
			t.Fatalf("first workspace lacks %s:\n%s", id, render(m.rows))
		}
	}

	m.newSpace()
	m.spaces[1].projects[scoped(localMachine, "r1")] = true
	m.rebuild()
	if shows(agents("later")) || shows(ops) {
		t.Fatalf("another workspace's folders show:\n%s", render(m.rows))
	}
	if !shows(agents("team")) {
		t.Fatalf("a folder holding a pane shown here is missing:\n%s", render(m.rows))
	}
	_, panes := m.shown(mach)
	in := m.spaces[1].filterFolders(m.folders, map[string][]proto.PaneInfo{localMachine: panes})
	if len(in[sshFolderKey()]) != 1 {
		t.Fatalf("SSH folder with a saved host left out: %+v", in)
	}

	// A folder made here shows here, empty, and in the first workspace.
	if !m.newFolder(localMachine, "r1", kindAgents, "mine") {
		t.Fatal("new folder refused")
	}
	m.rebuild()
	if !shows(agents("mine")) {
		t.Fatalf("own empty folder missing:\n%s", render(m.rows))
	}
	if m.newFolder(localMachine, "r1", kindAgents, "mine") || m.newFolder(localMachine, "r1", kindAgents, "team") {
		t.Fatal("a folder shown here was made twice")
	}
	// Naming one that is only another workspace's brings it here.
	if !m.newFolder(localMachine, "r1", kindAgents, "later") {
		t.Fatal("hidden folder's name refused")
	}
	m.rebuild()
	if !shows(agents("later")) || len(m.foldersIn(localMachine, "r1", kindAgents)) != 3 {
		t.Fatalf("later not brought here, or made twice: %+v\n%s", m.foldersIn(localMachine, "r1", kindAgents), render(m.rows))
	}
	m.switchSpace(0)
	if !shows(agents("mine")) {
		t.Fatalf("first workspace lacks the folder made in the second:\n%s", render(m.rows))
	}

	// Saved and restored with the workspace; removing a folder forgets it.
	saved := m.savedSpaces()
	if got := saved[0].Folders; len(got) != 2 {
		t.Fatalf("saved folders %q", got)
	}
	m.removeFolder(localMachine, "r1", kindAgents, "mine")
	if len(m.spaces[1].folders) != 1 {
		t.Fatalf("removed folder still listed: %v", m.spaces[1].folders)
	}
	m2, _ := a1Fixture(t, false)
	m2.restoreSpaces(saved, 0)
	if len(m2.spaces[1].folders) != 2 {
		t.Fatalf("restored folders %v", m2.spaces[1].folders)
	}
}
