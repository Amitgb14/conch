package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
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

	// The first workspace has its own: api, which was already there and is
	// shared now, and the panes no other workspace has — not those started
	// in the second.
	m.switchSpace(0)
	for id, want := range map[string]bool{"p1": true, "p3": true, "p7": true, "p5": false, "p6": false} {
		if got := indexOfRow(m.rows, paneNodeID(localMachine, id)) >= 0; got != want {
			t.Fatalf("first workspace: pane %s shown %v:\n%s", id, got, render(m.rows))
		}
	}

	// A project new to conch, added in the first, is the first's alone.
	msg = m.addedProject(localMachine, &proto.ProjectInfo{ID: "r9", Name: "x"}, "added ")().(spaceProjectMsg)
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "r9", Name: "x", Path: "/src/x"})
	m.receiveSpaceProject(msg)
	if m.spaces[1].projects[scoped(localMachine, "r9")] || !m.hasProject(0, scoped(localMachine, "r9")) {
		t.Fatal("r9 in the wrong workspace")
	}
	// And one new to conch added in the second is the second's alone: the
	// first does not keep it, as it keeps one that was already there.
	m.switchSpace(1)
	msg = m.addedProject(localMachine, &proto.ProjectInfo{ID: "r8", Name: "y"}, "added ")().(spaceProjectMsg)
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "r8", Name: "y", Path: "/src/y"})
	m.receiveSpaceProject(msg)
	if m.hasProject(0, scoped(localMachine, "r8")) || !m.hasProject(1, scoped(localMachine, "r8")) {
		t.Fatal("r8 in the wrong workspace")
	}
	if !m.hasProject(0, scoped(localMachine, "r1")) || !m.hasProject(1, scoped(localMachine, "r1")) {
		t.Fatal("api is not shared")
	}
	// A reply for a workspace closed since is only a flash.
	gone := newSpace()
	m.receiveSpaceProject(spaceProjectMsg{space: gone, machine: localMachine, info: proto.ProjectInfo{ID: "r1"}, flash: "added api"})
	if len(gone.projects) != 0 {
		t.Fatal("closed workspace changed")
	}
}

func TestSpaceRemoveProject(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.newSpace()
	key := scoped(localMachine, "r1")
	m.spaces[0].projects[key] = true // shared with the first
	m.spaces[1].projects[key] = true
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
	if indexOfRow(m.rows, projectNodeID(localMachine, "r1")) >= 0 || !m.hasProject(0, key) {
		t.Fatalf("not taken out, or taken from the first too:\n%s", render(m.rows))
	}
	peer.mu.Lock()
	for _, msg := range peer.msgs {
		if msg.Method == proto.MethodProjectRemove {
			t.Fatal("asked the server to remove a shared project")
		}
	}
	peer.mu.Unlock()

	// In no other workspace, x removes it from conch as it always did.
	delete(m.spaces[0].projects, key)
	m.spaces[1].projects[key] = true
	m.rebuild()
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.openRemove()
	d = m.overlay.(*dialog)
	if !strings.Contains(strings.Join(d.text, " "), "from the sidebar") {
		t.Fatalf("asks %q", d.text)
	}
	// And from the first, the same: it belongs there alone.
	m.switchSpace(0)
	m.spaces[1].projects = map[string]bool{}
	m.rebuild()
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.openRemove()
	if d = m.overlay.(*dialog); !strings.Contains(strings.Join(d.text, " "), "from the sidebar") {
		t.Fatalf("first asks %q", d.text)
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
	if !ok || !strings.Contains(strings.Join(d.text, " "), "nothing ends") {
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
	m2.restoreSpaces(st.FirstSpace, st.Spaces, st.ActiveSpace)
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
	m3.restoreSpaces(nil, nil, 3)
	if m3.spaces != nil || m3.activeSpace != 0 {
		t.Fatal("no saved spaces made some")
	}
	m3.restoreSpaces(nil, []savedSpace{{}}, 9) // an active one that isn't there
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
	// The first counts its own: api is the second's alone now, so what is
	// left is the two panes outside every project.
	mach.state = stateOnline
	m.switchSpace(0)
	if got := page(); !strings.Contains(got, "0 projects · 2 panes · 1 working · 0 waiting") {
		t.Fatalf("first workspace:\n%s", got)
	}
	if u := m.machineUsage(localMachine); u.empty() || u == all {
		t.Fatalf("first workspace usage %+v, everything %+v", u, all)
	}
	// With one workspace again, everything is counted.
	m.closeSpace(1)
	if got := page(); !strings.Contains(got, "4 panes") || m.machineUsage(localMachine) != all {
		t.Fatalf("one workspace:\n%s", got)
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
	// An SSH folder holding a saved host shows where that host does.
	if len(m.shownFolders()[sshFolderKey()]) != 0 {
		t.Fatalf("SSH folder of a host not here: %+v", m.shownFolders())
	}
	m.spaces[1].hosts["box"] = true
	if len(m.shownFolders()[sshFolderKey()]) != 1 {
		t.Fatalf("SSH folder of a host here left out: %+v", m.shownFolders())
	}

	// A folder made here shows here, empty, and only here.
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
	key := folderKey(localMachine, "r1", kindAgents)
	if m.hasFolder(0, key, savedFolder{Name: "mine"}) {
		t.Fatal("the first shows a folder made in the second")
	}
	// later, brought here, stays in the first that had it.
	if !m.hasFolder(0, key, savedFolder{Name: "later"}) || !m.hasFolder(0, key, savedFolder{Name: "ops"}) {
		t.Fatal("the first lost a folder of its own")
	}

	// Saved and restored with the workspace; removing a folder forgets it.
	saved := m.savedSpaces()
	if got := saved[0].Folders; len(got) != 2 || m.savedFirstSpace().Folders[0] != spaceFolder(key, "later") {
		t.Fatalf("saved folders %q", got)
	}
	m.removeFolder(localMachine, "r1", kindAgents, "mine")
	if len(m.spaces[1].folders) != 1 {
		t.Fatalf("removed folder still listed: %v", m.spaces[1].folders)
	}
	m2, _ := a1Fixture(t, false)
	m2.restoreSpaces(nil, saved, 0)
	if len(m2.spaces[1].folders) != 2 {
		t.Fatalf("restored folders %v", m2.spaces[1].folders)
	}
}

func TestSpaceWaitingCounter(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.panes[0].Agent.State = proto.AgentBlocked // p1, in api
	mach.panes[3].Agent.State = proto.AgentBlocked // p4, outside every project
	bar := func() string { return ansi.Strip(m.statusBar()) }
	if w, _ := m.inboxCount(); w != 2 || !strings.Contains(bar(), "2 waiting") {
		t.Fatalf("first workspace: %d waiting, bar %q", w, bar())
	}

	m.newSpace()
	if w, d := m.inboxCount(); w != 0 || d != 0 || strings.Contains(bar(), "⚑") {
		t.Fatalf("empty workspace: %d waiting %d done, bar %q", w, d, bar())
	}
	cursor := m.cursor
	m.jumpToAttention()
	if m.cursor != cursor || m.pendingShow != "" || !strings.Contains(m.flash, "in another") {
		t.Fatalf("! left the workspace: cursor %q, flash %q", m.cursor, m.flash)
	}

	m.spaces[1].projects[scoped(localMachine, "r1")] = true
	m.rebuild()
	if w, _ := m.inboxCount(); w != 1 || !strings.Contains(bar(), "1 waiting") {
		t.Fatalf("with api: %d waiting, bar %q", w, bar())
	}
	for range 2 { // round and round, never to p4
		m.jumpToAttention()
		if m.cursor != paneNodeID(localMachine, "p1") {
			t.Fatalf("! went to %q", m.cursor)
		}
		m.viewMachine, m.viewing = localMachine, "p1"
	}

	// Nothing waiting anywhere: the plain answer.
	mach.panes[0].Agent.State, mach.panes[3].Agent.State = proto.AgentIdle, proto.AgentIdle
	m.jumpToAttention()
	if m.flash != "no agents need you" {
		t.Fatalf("flash %q", m.flash)
	}
	m.switchSpace(0)
	mach.panes[3].Agent.State = proto.AgentBlocked
	if w, _ := m.inboxCount(); w != 1 {
		t.Fatalf("first workspace counts %d", w)
	}
}

func TestSpaceMachines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONCH_HOME", t.TempDir()) // removing a machine writes machines.json
	m, _ := a1Fixture(t, false)
	vm := newMachine("vm", "vm", "aghadge@vm")
	vm.state = stateOnline
	vm.projects = []proto.ProjectInfo{{ID: "r2", Name: "web", Path: "/srv/web"}}
	vm.panes = []proto.PaneInfo{{ID: "p1", Name: "zsh", State: proto.PaneRunning, ProjectID: "r2"}}
	m.machines = append(m.machines, vm)
	m.rebuild()
	shows := func(mid string) bool { return indexOfRow(m.rows, machineID(mid)) >= 0 }
	if !shows("vm") {
		t.Fatalf("first workspace lacks vm:\n%s", render(m.rows))
	}

	// A new workspace lists this computer and nothing else.
	m.newSpace()
	if !shows(localMachine) || shows("vm") {
		t.Fatalf("new workspace:\n%s", render(m.rows))
	}
	if got := ansi.Strip(strings.Join(m.sandboxesLines("", 90), "\n")); !strings.Contains(got, "none yet") {
		t.Fatalf("sandboxes page counts machines not here:\n%s", got)
	}

	// M brings one conch already has.
	a1At(t, m, machineID(localMachine))
	a1Key(t, m, runes("M"))
	mu := m.overlay.(*menu)
	last := mu.items[len(mu.items)-1]
	if last.key != "e" || !strings.Contains(last.label, "already in conch") {
		t.Fatalf("M menu %+v", mu.items)
	}
	last.run(m)
	bring := m.overlay.(*menu)
	if len(bring.items) != 1 || bring.items[0].label != "vm" || bring.back == nil {
		t.Fatalf("bring menu %+v", bring.items)
	}
	m.overlay = nil
	bring.items[0].run(m)
	if !shows("vm") || m.cursor != machineID("vm") {
		t.Fatalf("vm not brought:\n%s", render(m.rows))
	}
	if indexOfRow(m.rows, projectNodeID("vm", "r2")) >= 0 {
		t.Fatalf("vm's projects came with it:\n%s", render(m.rows))
	}
	a1Key(t, m, runes("M"))
	if mu := m.overlay.(*menu); strings.Contains(mu.items[len(mu.items)-1].label, "already in conch") {
		t.Fatal("bring offered with nothing to bring")
	}
	m.overlay = nil

	// x takes it out of the workspace, with its projects here, and conch
	// keeps it.
	m.spaces[1].projects[scoped("vm", "r2")] = true
	m.spaces[1].projects[scoped(localMachine, "r1")] = true
	m.rebuild()
	a1At(t, m, machineID("vm"))
	m.openRemove()
	d := m.overlay.(*dialog)
	if !strings.Contains(strings.Join(d.text, " "), "out of this workspace") {
		t.Fatalf("remove asks %q", d.text)
	}
	m.overlay = nil
	d.submit(m, nil)
	if shows("vm") || m.machine("vm") == nil || len(m.spaces[1].projects) != 1 {
		t.Fatalf("after taking out: projects %v\n%s", m.spaces[1].projects, render(m.rows))
	}

	// Holding a project or a pane there lists it without being added.
	m.spaces[1].panes[paneKey("vm", "p1")] = true
	m.rebuild()
	if !shows("vm") {
		t.Fatalf("a pane there didn't list vm:\n%s", render(m.rows))
	}
	delete(m.spaces[1].panes, paneKey("vm", "p1"))

	// A machine added from here joins it; this computer can't be taken out.
	next, _ := m.Update(machineAddedMsg{m: remote.Machine{ID: "box", Label: "box", Target: "box"}})
	*m = next.(Model)
	if !m.spaces[1].machines["box"] || !shows("box") {
		t.Fatalf("added machine not here:\n%s", render(m.rows))
	}
	a1At(t, m, machineID(localMachine))
	m.openRemove()
	if m.overlay != nil {
		t.Fatal("offered to take this computer out")
	}

	// Saved with the workspace; the first still lists everything.
	if saved := m.savedSpaces(); len(saved[0].Machines) != 1 || saved[0].Machines[0] != "box" {
		t.Fatalf("saved machines %v", saved[0].Machines)
	}
	m2, _ := a1Fixture(t, false)
	m2.restoreSpaces(nil, m.savedSpaces(), 0)
	if !m2.spaces[1].machines["box"] {
		t.Fatal("machines not restored")
	}
	// The first lists vm, whose project went back to being nobody's, and
	// not box, added in the second.
	m.switchSpace(0)
	if !shows("vm") || shows("box") {
		t.Fatalf("first workspace:\n%s", render(m.rows))
	}
	// Brought into the first, box is listed there too and kept in the second.
	m.openBringMachine(nil)
	bring = m.overlay.(*menu)
	if len(bring.items) != 1 || bring.items[0].label != "box" {
		t.Fatalf("bring offers %+v", bring.items)
	}
	m.overlay = nil
	bring.items[0].run(m)
	if !shows("box") || !m.spaces[1].machines["box"] {
		t.Fatalf("box not shared:\n%s", render(m.rows))
	}
	// Removed from conch, it is forgotten by every workspace.
	m.removeMachine("box")
	if m.spaces[0].machines["box"] || m.spaces[1].machines["box"] {
		t.Fatal("removed machine still listed")
	}
}

func TestSpaceCloseMovesToFirst(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	m.savedSSH = []string{"old", "box"}
	m.newSpace()
	s := m.spaces[1]
	s.projects[scoped(localMachine, "r1")] = true
	s.panes[paneKey(localMachine, "p4")] = true
	s.hosts["box"] = true
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p4"))
	m.switchSpace(0)
	// The first lacks what the second has, and keeps what is nobody's.
	if m.showsHost("box") || !m.showsHost("old") || indexOfRow(m.rows, paneNodeID(localMachine, "p4")) >= 0 {
		t.Fatalf("first before close:\n%s", render(m.rows))
	}
	tabs := len(m.tabs)

	m.switchSpace(1)
	m.closeSpace(1)
	if m.spaces != nil || m.activeSpace != 0 {
		t.Fatalf("on %d of %v", m.activeSpace, m.spaces)
	}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if indexOfRow(m.rows, paneNodeID(localMachine, id)) < 0 {
			t.Fatalf("lacks %s after close:\n%s", id, render(m.rows))
		}
	}
	if !m.showsHost("box") || len(m.tabs) != tabs+1 || mach.pane("p4") == nil {
		t.Fatalf("host, tab or pane lost: tabs %d → %d", tabs, len(m.tabs))
	}

	// Closed with three, the rest stay apart and the first gets the lot.
	m.newSpace()
	m.spaces[1].hosts["box"] = true
	m.newSpace()
	m.spaces[2].hosts["old"] = true
	m.closeSpace(1)
	if !m.spaces[0].hosts["box"] || m.hasHost(0, "old") || !m.hasHost(1, "old") {
		t.Fatalf("after closing one of three: first %v, other %v", m.spaces[0].hosts, m.spaces[1].hosts)
	}
}

func TestSpaceSavedHosts(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.savedSSH = []string{"old"}
	hosts := func() []string { return m.shownHosts() }
	// With one workspace, every saved host is listed and no other is.
	if !m.showsHost("old") || m.showsHost("unsaved") {
		t.Fatal("one workspace: wrong hosts listed")
	}

	m.newSpace() // a host saved before any workspace is the first's
	if len(hosts()) != 0 {
		t.Fatalf("new workspace lists %v", hosts())
	}
	m.saveSSH("new", nil)
	if got := hosts(); len(got) != 1 || got[0] != "new" {
		t.Fatalf("second lists %v", got)
	}
	if m.hasHost(0, "new") {
		t.Fatal("a host saved in the second shows in the first")
	}
	// Saving one the first has lists it here as well; the first keeps it.
	m.saveSSH("old", nil)
	if !m.hasHost(1, "old") || !m.hasHost(0, "old") {
		t.Fatalf("old: first %v second %v", m.hasHost(0, "old"), m.hasHost(1, "old"))
	}
	// So does adding it through the dialog's way in, instead of "already saved".
	m.switchSpace(0)
	if err := m.putSSHHost("", "new", sshHostInfo{Name: "nu"}, ""); err != nil {
		t.Fatal(err)
	}
	if !m.hasHost(0, "new") || m.sshInfo["new"].Name != "nu" || len(m.savedSSH) != 2 {
		t.Fatalf("bring through put: %v %+v", m.savedSSH, m.sshInfo)
	}
	if err := m.putSSHHost("", "new", sshHostInfo{}, ""); err == nil {
		t.Fatal("saving one listed here again was not refused")
	}
	// Editing a host carries its place in every workspace.
	if err := m.putSSHHost("new", "newer", sshHostInfo{}, ""); err != nil {
		t.Fatal(err)
	}
	if !m.hasHost(0, "newer") || !m.hasHost(1, "newer") || m.spaces[1].hosts["new"] {
		t.Fatalf("rename: %v %v", m.spaces[0].hosts, m.spaces[1].hosts)
	}
	// Connecting to one only another workspace has asks to save it here.
	m.switchSpace(1)
	delete(m.spaces[1].hosts, "old")
	m.connectSSHWith("old", nil)
	if mu, ok := m.overlay.(*menu); !ok || !strings.Contains(mu.title, "Save") {
		t.Fatalf("overlay %#v", m.overlay)
	}
	m.overlay = nil

	// x: out of this workspace while another has it, forgotten when none does.
	m.rebuild()
	a1At(t, m, savedSSHID("newer"))
	m.openRemove()
	d := m.overlay.(*dialog)
	if !strings.Contains(strings.Join(d.text, " "), "out of this workspace") {
		t.Fatalf("asks %q", d.text)
	}
	m.overlay = nil
	d.submit(m, nil)
	if m.hasHost(1, "newer") || !m.hasHost(0, "newer") {
		t.Fatal("newer not taken out of the second only")
	}
	m.switchSpace(0)
	a1At(t, m, savedSSHID("newer"))
	m.openRemove()
	d = m.overlay.(*dialog)
	if !strings.Contains(strings.Join(d.text, " "), "Forget") {
		t.Fatalf("asks %q", d.text)
	}
	m.overlay = nil
	m.spaces[1].hosts["newer"] = true // a stale entry the forget must clear
	d.submit(m, nil)
	if slices.Contains(m.savedSSH, "newer") || m.spaces[1].hosts["newer"] {
		t.Fatal("not forgotten everywhere")
	}

	// Saved and restored, the first's own list included.
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	m.saveState()()
	st := loadUIState(m.statePath)
	if st.FirstSpace == nil || !slices.Contains(st.FirstSpace.Hosts, "old") || len(st.FirstSpace.Tabs) != 0 {
		t.Fatalf("first space saved %+v", st.FirstSpace)
	}
	m2, _ := a1Fixture(t, false)
	m2.savedSSH = slices.Clone(m.savedSSH)
	m2.restoreSpaces(st.FirstSpace, st.Spaces, st.ActiveSpace)
	if !m2.spaces[0].hosts["old"] {
		t.Fatalf("first space restored %v", m2.spaces[0].hosts)
	}
}
