package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// a1FourTabs opens p1..p4 in a tab each: t0 p1 (api agents), t1 p2 (api
// terminals), t2 p3 (CLI terminals), t3 p4 (CLI agents).
func a1FourTabs(t *testing.T, m *Model) {
	t.Helper()
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		a1Open(t, m, paneNodeID(localMachine, id))
	}
	if len(m.tabs) != 4 {
		t.Fatalf("tabs: %d", len(m.tabs))
	}
	for i, id := range []string{"p1", "p2", "p3", "p4"} {
		if got := m.tabs[i].root.leaves()[0].view.PaneID; got != id {
			t.Fatalf("tab %d shows %q, want %q", i, got, id)
		}
	}
}

func TestA1MachineRowListsNoTabsAndPreviews(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, machineID(localMachine))
	if vis := m.visibleTabs(); len(vis) != 0 {
		t.Fatalf("machine row lists tabs %v", vis)
	}
	if !m.previewing || m.tab().focused().view.Kind != kindMachine {
		t.Fatalf("machine not previewed: previewing %v view %+v", m.previewing, m.tab().focused().view)
	}
	// Even the tab bar is empty apart from the + button.
	bar, hits := m.tabBar(80)
	if len(hits) != 1 || hits[0].tab != -1 || !strings.Contains(ansi.Strip(bar), "+") {
		t.Fatalf("tab bar on the machine row: %q %+v", ansi.Strip(bar), hits)
	}
	// stepping has nothing to step through
	if cmd := m.stepTab(1); cmd != nil || !m.previewing {
		t.Fatal("stepTab moved off the machine preview")
	}
}

func TestA1ScopeFilters(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	cases := []struct {
		row  string
		want string
	}{
		{workspaceID(localMachine), "p1,p2"}, // every project's tabs, not CLI's
		{projectNodeID(localMachine, "r1"), "p1,p2"},
		{sectionID(localMachine, "r1", "agents"), "p1"},
		{sectionID(localMachine, "r1", "terminals"), "p2"},
		{sectionID(localMachine, "r1", "branches"), ""}, // branches are about changes, not panes
		{cliID(localMachine), "p3,p4"},
		{machineID(localMachine) + "/agents", "p4"},
		{looseTerminalsID(localMachine), "p3"},
		{paneNodeID(localMachine, "p3"), "p3"},
		{paneNodeID(localMachine, "p1"), "p1"},
	}
	for _, tc := range cases {
		a1At(t, m, tc.row)
		if got := strings.Join(a1PaneIDs(m, m.visibleTabs()), ","); got != tc.want {
			t.Errorf("%s lists %q, want %q", tc.row, got, tc.want)
		}
		r := m.rows[indexOfRow(m.rows, tc.row)]
		switch {
		case pageRow(r.kind) && (!m.previewing || m.tab().focused().view.Row != tc.row):
			t.Errorf("%s is a page: it should show itself, previewing %v", tc.row, m.previewing)
		case !pageRow(r.kind) && m.previewing:
			t.Errorf("%s previews although its group has tabs", tc.row)
		}
	}
}

func TestA1ScopeNames(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if got := m.scopeName(tabScope{level: scopeProject, machine: localMachine, project: "r1", section: kindAgents}); got != "api · agents" {
		t.Fatalf("project agents: %q", got)
	}
	if got := m.scopeName(tabScope{level: scopeCLI, machine: localMachine, section: kindTerminals}); got != "CLI · terminals" {
		t.Fatalf("CLI terminals: %q", got)
	}
	if got := m.scopeName(tabScope{level: scopeWorkspace, machine: localMachine}); got != "Workspace" {
		t.Fatalf("workspace: %q", got)
	}
	if got := m.scopeName(tabScope{level: scopeMachine, machine: localMachine}); got != "" {
		t.Fatalf("machine scope named %q", got)
	}
	if got := m.scopeName(tabScope{}); got != "" {
		t.Fatalf("no scope named %q", got)
	}
	// With several machines, the machine is named too.
	m.machines = append(m.machines, newMachine("box", "buildbox", "me@box"))
	if got := m.scopeName(tabScope{level: scopeCLI, machine: "box"}); got != "buildbox › CLI" {
		t.Fatalf("remote CLI: %q", got)
	}
	// An unknown project in project scope yields no project name.
	if got := m.scopeName(tabScope{level: scopeProject, machine: localMachine, project: "zz"}); got != "local › " {
		t.Fatalf("unknown project: %q", got)
	}
}

func TestA1InScope(t *testing.T) {
	proj := tabScope{level: scopeProject, machine: "local", project: "r1"}
	agents := proj
	agents.section = kindAgents
	terms := proj
	terms.section = kindTerminals
	mach := tabScope{level: scopeMachine, machine: "local"}
	cli := tabScope{level: scopeCLI, machine: "local", section: kindAgents}
	for _, tc := range []struct {
		name string
		f, s tabScope
		want bool
	}{
		{"empty tab under project", proj, tabScope{}, true},
		{"empty tab under machine", mach, tabScope{}, false},
		{"machine filter", mach, agents, false},
		{"machine tab", proj, mach, false},
		{"project lists its sections", proj, agents, true},
		{"agents filter drops terminals", agents, terms, false},
		{"agents filter keeps agents", agents, agents, true},
		{"CLI is not the project", proj, cli, false},
		{"other machine", tabScope{level: scopeCLI, machine: "box"}, cli, false},
		{"workspace lists projects", tabScope{level: scopeWorkspace, machine: "local"}, agents, true},
		{"workspace drops CLI", tabScope{level: scopeWorkspace, machine: "local"}, cli, false},
		{"workspace of another machine", tabScope{level: scopeWorkspace, machine: "box"}, proj, false},
		{"empty tab under workspace", tabScope{level: scopeWorkspace, machine: "local"}, tabScope{}, true},
		{"workspace tab under workspace", tabScope{level: scopeWorkspace, machine: "local"}, tabScope{level: scopeWorkspace, machine: "local"}, true},
		{"workspace tab under a project", proj, tabScope{level: scopeWorkspace, machine: "local"}, false},
	} {
		if got := inScope(tc.f, tc.s); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestA1PreviewBecomesTabWhenShownOrTyped(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1At(t, m, cliID(localMachine))
	if !m.previewing || len(m.tabs) != 0 || m.tab().focused().view.Kind != kindCLI {
		t.Fatalf("CLI without tabs should preview: %v %d", m.previewing, len(m.tabs))
	}
	// Typing into the preview (focus on the main area) keeps it as a tab.
	m.focus = focusMain
	next, _ := m.Update(tickMsg{})
	nm := next.(Model)
	if nm.previewing || len(nm.tabs) != 1 || nm.tabs[0].home.level != scopeCLI {
		t.Fatalf("typing did not promote: previewing %v tabs %d", nm.previewing, len(nm.tabs))
	}

	// Showing a row in a preview promotes it, too.
	m2, _ := a1Fixture(t, false)
	a1At(t, m2, paneNodeID(localMachine, "p3"))
	if !m2.previewing {
		t.Fatal("pane without a tab should preview")
	}
	a1Open(t, m2, paneNodeID(localMachine, "p3"))
	if m2.previewing || len(m2.tabs) != 1 || m2.tab().focused().view.PaneID != "p3" {
		t.Fatalf("show did not promote: %v %d", m2.previewing, len(m2.tabs))
	}

	// Splitting the preview promotes it and adds an empty half.
	m3, _ := a1Fixture(t, false)
	a1At(t, m3, projectNodeID(localMachine, "r1"))
	if !m3.previewing {
		t.Fatal("project without tabs should preview")
	}
	m3.split(splitRight, viewRef{})
	if m3.previewing || len(m3.tabs) != 1 || len(m3.tab().root.leaves()) != 2 || !m3.tab().focused().pick {
		t.Fatalf("split preview: previewing %v tabs %d", m3.previewing, len(m3.tabs))
	}
}

func TestA1PromoteEmptyPreviewIsDropped(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	m.activeTab = 2
	m.previewing = true
	m.preview = &tab{root: &layoutNode{leaf: m.newLeaf(viewRef{})}}
	m.promote()
	if m.previewing || m.preview != nil || len(m.tabs) != 4 || m.activeTab != 2 {
		t.Fatalf("empty preview became a tab: %d tabs, active %d", len(m.tabs), m.activeTab)
	}
	// Not previewing: nothing to do.
	m.promote()
	if len(m.tabs) != 4 {
		t.Fatal("promote without a preview changed tabs")
	}
}

func TestA1PickTabRemembersGroupTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	proj := cliID(localMachine) // CLI lists p3 and p4
	a1At(t, m, proj)
	if m.activeTab != 3 || m.previewing {
		t.Fatalf("CLI keeps the active tab it lists (p4), got %d", m.activeTab)
	}
	// Choosing p3's tab from the tree records it for the CLI group.
	m.switchTab(2)
	if m.activeTab != 2 || m.scopeTab[m.rowScope(m.rows[indexOfRow(m.rows, proj)]).key()] != m.tabs[2] {
		t.Fatalf("scope tab not remembered: active %d", m.activeTab)
	}
	a1At(t, m, sectionID(localMachine, "r1", "agents"))
	if m.activeTab != 0 {
		t.Fatalf("api agents picks p1, got %d", m.activeTab)
	}
	a1At(t, m, proj)
	if m.activeTab != 2 {
		t.Fatalf("back on CLI the last used tab returns, got %d", m.activeTab)
	}
	// Within the group the active tab stays while it is listed.
	a1At(t, m, looseTerminalsID(localMachine))
	if m.activeTab != 2 || m.previewing {
		t.Fatalf("moving within the group changed the tab to %d", m.activeTab)
	}
	// A page of a project shows itself, and a browsing tab of the group
	// is reused for it.
	branches := sectionID(localMachine, "r1", "branches")
	a1At(t, m, branches)
	if !m.previewing || m.tab().focused().view.Row != branches {
		t.Fatalf("branches page: previewing %v", m.previewing)
	}
	m.promote() // keep it as a browsing tab
	browse := m.activeTab
	a1At(t, m, sectionID(localMachine, "r1", "agents"))
	a1At(t, m, projectNodeID(localMachine, "r1"))
	if m.previewing || m.activeTab != browse || m.tab().focused().view.Row != projectNodeID(localMachine, "r1") {
		t.Fatalf("project page: previewing %v active %d (browsing tab %d)", m.previewing, m.activeTab, browse)
	}
	// keepTab: a redraw right after a pick keeps the chosen tab even though
	// the cursor names a different group.
	m.gotoTab(3)
	m.cursor = proj
	m.keepTab = true
	m.syncView()
	if m.activeTab != 3 {
		t.Fatalf("keepTab lost the pick: %d", m.activeTab)
	}
	// pickedFor: redraws with the cursor unchanged don't pick again.
	m.syncView()
	if m.activeTab != 3 || m.pickedFor != proj {
		t.Fatalf("a redraw picked again: %d, pickedFor %q", m.activeTab, m.pickedFor)
	}
}

func TestA1StepAndGotoVisibleTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, cliID(localMachine))
	if vis := m.visibleTabs(); len(vis) != 2 || vis[0] != 2 || vis[1] != 3 || m.activeTab != 3 {
		t.Fatalf("CLI tabs %v active %d", vis, m.activeTab)
	}
	m.stepTab(1)
	if m.activeTab != 2 {
		t.Fatalf("next wraps: %d", m.activeTab)
	}
	m.stepTab(1)
	if m.activeTab != 3 {
		t.Fatalf("next: %d", m.activeTab)
	}
	m.stepTab(-1)
	if m.activeTab != 2 {
		t.Fatalf("previous: %d", m.activeTab)
	}
	// From a preview, n goes to the first listed tab and p to the last.
	m.previewing = true
	m.stepTab(-1)
	if m.previewing || m.activeTab != 3 {
		t.Fatalf("p from preview: %d", m.activeTab)
	}
	m.previewing = true
	m.stepTab(1)
	if m.previewing || m.activeTab != 2 {
		t.Fatalf("n from preview: %d", m.activeTab)
	}
	if cmd := m.gotoVisibleTab(5); cmd != nil || m.activeTab != 2 {
		t.Fatal("goto a tab past the bar moved")
	}
	m.gotoVisibleTab(1)
	if m.activeTab != 3 {
		t.Fatalf("goto second listed: %d", m.activeTab)
	}
	// switchTab ignores out of range tabs, and from the main area focuses
	// the split (gotoTab), which moves the cursor onto what it shows.
	if m.switchTab(9) != nil || m.gotoTab(-1) != nil {
		t.Fatal("out of range switch")
	}
	m.focus = focusMain
	m.switchTab(0)
	if m.activeTab != 0 || m.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("switch from main: tab %d cursor %q", m.activeTab, m.cursor)
	}
}

func TestA1CloseTabDownToZero(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	m.gotoTab(3)
	if m.closeTab(7) != nil || m.closeTab(-1) != nil {
		t.Fatal("closing a missing tab did something")
	}
	m.closeTab(1) // before the active: it shifts left
	if len(m.tabs) != 3 || m.activeTab != 2 || m.tab().focused().view.PaneID != "p4" {
		t.Fatalf("after closing 1: %d tabs active %d", len(m.tabs), m.activeTab)
	}
	m.closeTab(2) // the active, last one
	if len(m.tabs) != 2 || m.activeTab != 1 {
		t.Fatalf("after closing active: %d tabs active %d", len(m.tabs), m.activeTab)
	}
	m.closeTab(0)
	m.closeTab(0)
	if len(m.tabs) != 0 || m.activeTab != 0 {
		t.Fatalf("tabs left: %d active %d", len(m.tabs), m.activeTab)
	}
	if !m.previewing || m.tab() == nil {
		t.Fatal("no tabs should leave a preview on screen")
	}
	// closeLeaf on a lone preview does nothing.
	if m.closeLeaf() != nil || !m.previewing {
		t.Fatal("closeLeaf closed the preview")
	}
}

func TestA1CloseLeafAndSplits(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	tb := m.tab()
	if len(tb.root.leaves()) != 2 || tb.focused().view.PaneID != "p2" {
		t.Fatal("split setup")
	}
	m.focus = focusMain
	m.closeLeaf()
	if len(m.tabs) != 1 || len(m.tab().root.leaves()) != 1 || m.tab().focused().view.PaneID != "p1" || m.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("closeLeaf: leaves %d cursor %q", len(m.tab().root.leaves()), m.cursor)
	}
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	if len(m.tabs) != 2 {
		t.Fatalf("p3 tab: %d", len(m.tabs))
	}
	m.closeLeaf() // the only leaf of a tab closes the tab
	if len(m.tabs) != 1 {
		t.Fatalf("closing the lone leaf left %d tabs", len(m.tabs))
	}
}

func TestA1LastSplitAndLastTabFlash(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	if m.lastSplit() != nil || !m.flashIsErr || m.flash != "no previous split" {
		t.Fatalf("lastSplit: %q", m.flash)
	}
	if m.gotoLastTab() != nil || m.flash != "no previous tab" {
		t.Fatalf("gotoLastTab: %q", m.flash)
	}
}

func TestA1ToggleSync(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.toggleSync()
	if m.tab().sync || !m.flashIsErr {
		t.Fatal("sync with a single pane")
	}
	m.split(splitDown, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.toggleSync()
	if !m.tab().sync || m.flashIsErr || !strings.Contains(m.flash, "all 2 splits") {
		t.Fatalf("sync on: %v %q", m.tab().sync, m.flash)
	}
	// Once on it turns off even if panes are gone.
	m.machines[0].panes[0].State = proto.PaneExited
	m.toggleSync()
	if m.tab().sync || m.flash != "typing goes to the focused split only" {
		t.Fatalf("sync off: %q", m.flash)
	}
}

func TestA1ForwardSynced(t *testing.T) {
	m, peer := a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.split(splitDown, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p3"))]))
	c := m.machines[0].c
	// Not synchronized: nothing forwarded.
	m.forwardSynced(localMachine, "p3", runes("a"))
	if n := peer.count(t, c, proto.MethodPaneSendText, ""); n != 0 {
		t.Fatalf("forwarded while not synced: %d", n)
	}
	m.tab().sync = true
	m.forwardSynced(localMachine, "p3", runes("z"))
	if n := peer.count(t, c, proto.MethodPaneSendText, `"text":"z"`); n != 2 {
		t.Fatalf("synced key reached %d other panes, want 2", n)
	}
	if n := peer.count(t, c, proto.MethodPaneSendText, `"id":"p3"`); n != 0 {
		t.Fatal("the typed-in pane got its own key twice")
	}
}

func TestA1SyncViewSubscribesAndResizes(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c := m.machines[0].c
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	peer.waitMethod(t, proto.MethodPaneSubscribe, `"id":"p1"`)
	peer.waitMethod(t, proto.MethodPaneResize, `"id":"p1"`)
	if !m.subscribed[paneKey(localMachine, "p1")] || m.viewing != "p1" || m.viewMachine != localMachine {
		t.Fatalf("not viewing p1: %q", m.viewing)
	}
	// Same size again: no second resize.
	m.syncView()
	if n := peer.count(t, c, proto.MethodPaneResize, `"id":"p1"`); n != 1 {
		t.Fatalf("resizes: %d", n)
	}
	// The frame of the viewed pane is picked up with its offset.
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Offset: 3, History: 10}
	m.syncView()
	if m.frame == nil || m.offset != 3 {
		t.Fatalf("frame/offset: %v %d", m.frame, m.offset)
	}
	// Moving to another tab unsubscribes p1.
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	peer.waitMethod(t, proto.MethodPaneUnsubscribe, `"id":"p1"`)
	if m.subscribed[paneKey(localMachine, "p1")] || m.frames[paneKey(localMachine, "p1")] != nil {
		t.Fatal("p1 still subscribed")
	}
}

func TestA1RestoreTabsDropsMachineAndEmptyTabs(t *testing.T) {
	pane := viewRef{Row: "pane:p1", Kind: kindPane, Machine: localMachine, PaneID: "p1"}
	machine := viewRef{Row: "m:local", Kind: kindMachine, Machine: localMachine}
	saved := []savedTab{
		{Name: "machine", Root: &savedNode{View: &machine, Focus: true}},
		{Name: "empty", Root: &savedNode{}},
		{Name: "broken", Root: &savedNode{Split: "right", A: &savedNode{View: &pane}}}, // B missing
		{Name: "nil"},
		{Name: "work", Root: &savedNode{Split: "down", Ratio: 7, A: &savedNode{View: &machine}, B: &savedNode{View: &pane, Focus: true}}, Sync: true},
	}
	var m Model
	m.restoreTabs(saved, 4)
	if len(m.tabs) != 1 || m.tabs[0].name != "work" || m.activeTab != 0 || !m.tabs[0].sync {
		t.Fatalf("restored %d tabs", len(m.tabs))
	}
	if r := m.tabs[0].root; r.dir != splitDown || r.ratio != 0.5 || m.tabs[0].focused().view.PaneID != "p1" {
		t.Fatalf("restored layout: dir %v ratio %v", r.dir, r.ratio)
	}
	if m.previewing {
		t.Fatal("a restored tab should be on screen, not the preview")
	}
	// An active index naming a dropped tab falls back to the first.
	var m2 Model
	m2.restoreTabs(saved, 0)
	if m2.activeTab != 0 || len(m2.tabs) != 1 {
		t.Fatalf("fallback: %d", m2.activeTab)
	}
	// Nothing worth restoring: the preview is on screen.
	var m3 Model
	m3.restoreTabs(saved[:2], 0)
	if len(m3.tabs) != 0 || !m3.previewing {
		t.Fatal("only machine/empty tabs should leave a preview")
	}
}

func TestA1TabBarAndLabels(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, cliID(localMachine))
	bar, hits := m.tabBar(80)
	plain := ansi.Strip(bar)
	if ansi.StringWidth(bar) != 80 {
		t.Fatalf("bar width %d", ansi.StringWidth(bar))
	}
	if !strings.Contains(plain, " 1 bash ") || !strings.Contains(plain, " 2 codex ") || !strings.Contains(plain, "× ") {
		t.Fatalf("bar: %q", plain)
	}
	// hits: tab 2, tab 3 (active, with its close button), new
	var kinds []int
	for _, h := range hits {
		kinds = append(kinds, h.tab)
		if h.x1 <= h.x0 {
			t.Fatalf("empty hit %+v", h)
		}
	}
	if len(kinds) != 4 || kinds[0] != 2 || kinds[1] != 3 || kinds[2] != -2 || kinds[3] != -1 {
		t.Fatalf("hits %v", kinds)
	}
	// Too narrow: items that don't fit are left out whole.
	bar, _ = m.tabBar(5)
	if ansi.StringWidth(bar) != 5 {
		t.Fatalf("narrow bar %q", ansi.Strip(bar))
	}
	if itoa(3) != "3" || itoa(12) != "12" || itoa(0) != "0" {
		t.Fatal("itoa")
	}

	// Labels: custom name, empty leaves, splits, long names truncated.
	tb := m.tabs[0]
	tb.name = "mine"
	if m.tabLabel(tb) != "mine" {
		t.Fatal("custom name")
	}
	tb.name = ""
	m.machines[0].panes[0].Name = strings.Repeat("x", 40)
	if got := m.tabLabel(tb); ansi.StringWidth(got) != 20 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long label %q", got)
	}
	empty := &tab{root: &layoutNode{leaf: &leaf{id: 99}}, focus: 99}
	if m.tabLabel(empty) != "empty" {
		t.Fatalf("empty label %q", m.tabLabel(empty))
	}
	for _, tc := range []struct {
		v    viewRef
		want string
	}{
		{viewRef{Row: "b", Kind: kindBranch, Branch: "feat"}, "feat"},
		{viewRef{Row: "p", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"}, "api"},
		{viewRef{Row: "p", Kind: kindProject, Machine: localMachine, ProjectID: "gone"}, "empty"},
		{viewRef{Row: "m", Kind: kindMachine, Machine: localMachine}, "local"},
		{viewRef{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "gone"}, "empty"},
		{viewRef{Kind: kindMachine, Machine: localMachine}, "empty"},
	} {
		if got := m.viewLabel(tc.v); got != tc.want {
			t.Errorf("viewLabel(%+v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestA1TabPickerGroupsLikeTheTree(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// Open CLI first, then the project: groups keep first-appearance order,
	// so both CLI tabs come before both project tabs even though they
	// were opened interleaved.
	a1Open(t, m, paneNodeID(localMachine, "p3"))
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	a1Open(t, m, paneNodeID(localMachine, "p4"))
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.gotoTab(1) // p1
	m.split(splitRight, viewRef{})
	m.openTabPicker()
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("overlay %T", m.overlay)
	}
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, ansi.Strip(it.label))
	}
	got := strings.Join(labels, "|")
	// Groups: CLI terminals (p3), api agents (p1 + split), CLI agents (p4),
	// api terminals (p2) — each section is its own group.
	want := "bash · a1  CLI · terminals|claude ⊞  api · agents|  ├ claude · feat · idle|  └ empty|codex · working  CLI · agents|zsh · api  api · terminals"
	if got != want {
		t.Fatalf("picker:\n%s\nwant:\n%s", got, want)
	}
	if mu.sel != 3 {
		t.Fatalf("selected %d, want the focused split (3)", mu.sel)
	}
	// Running a tab item goes there; a split item of a tab that has since
	// gone does nothing.
	mu.items[4].run(m)
	if m.activeTab != 2 {
		t.Fatalf("picked codex tab: %d", m.activeTab)
	}
	split := mu.items[2].run
	m.closeTab(1)
	if split(m) != nil {
		t.Fatal("stale split item acted")
	}
}

func TestA1CloseAskConfirms(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	// A running pane asks first.
	if m.closeSplitAsk() != nil {
		t.Fatal("closeSplitAsk returned a command before confirming")
	}
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(d.text[0], "Close claude?") {
		t.Fatalf("confirm: %#v", m.overlay)
	}
	// Confirming calls pane.close, offline here: an error message.
	if msg := d.submit(m, nil)(); msg == nil {
		t.Fatal("confirm produced nothing")
	} else if e, ok := msg.(errMsg); !ok || !strings.Contains(e.err.Error(), "local is") {
		t.Fatalf("offline close: %#v", msg)
	}
	m.overlay = nil
	// An exited pane closes without asking.
	m.machines[0].panes[0].State = proto.PaneExited
	if m.closeSplitAsk() == nil || m.overlay != nil {
		t.Fatal("exited pane should close without asking")
	}
	// A non-pane split just closes.
	m.split(splitRight, viewRef{})
	if len(m.tab().root.leaves()) != 2 {
		t.Fatal("split")
	}
	m.closeSplitAsk()
	if len(m.tab().root.leaves()) != 1 || m.overlay != nil {
		t.Fatal("empty split did not close")
	}

	// closeTabAsk names the running panes.
	m.machines[0].panes[0].State = proto.PaneRunning
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p3"))]))
	if m.closeTabAsk(-1) != nil || m.closeTabAsk(9) != nil {
		t.Fatal("closeTabAsk out of range")
	}
	m.closeTabAsk(m.activeTab)
	d, ok = m.overlay.(*dialog)
	if !ok || !strings.Contains(d.text[0], "It ends zsh, bash.") {
		t.Fatalf("closeTabAsk: %#v", m.overlay)
	}
	before := len(m.tabs)
	m.overlay = nil
	d.submit(m, nil)
	if len(m.tabs) != before-1 {
		t.Fatalf("confirmed close left %d tabs (had %d)", len(m.tabs), before)
	}
	// With nothing running the tab closes straight away, but never the last.
	m.machines[0].panes[0].State = proto.PaneExited
	m.closeTabAsk(0)
	if m.overlay != nil || len(m.tabs) != 1 {
		t.Fatalf("last tab: overlay %T tabs %d", m.overlay, len(m.tabs))
	}
}

func TestA1SwapSplitNeedsTwo(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	if m.swapSplit(1) != nil {
		t.Fatal("swap with one leaf")
	}
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.swapSplit(1) // wraps from the last to the first
	ls := m.tab().root.leaves()
	if ls[0].view.PaneID != "p2" || ls[1].view.PaneID != "p1" || m.tab().focus != ls[0].id {
		t.Fatalf("swap: %q %q", ls[0].view.PaneID, ls[1].view.PaneID)
	}
	m.tab().focus = 12345
	if m.swapSplit(1) != nil {
		t.Fatal("swap with a missing focus")
	}
}

func TestA1ResizeKeyAndRepeat(t *testing.T) {
	for key, want := range map[string][3]int{
		"ctrl+left": {-1, 0, 1}, "ctrl+right": {1, 0, 1}, "alt+up": {0, -5, 1}, "alt+down": {0, 5, 1},
		"ctrl+x": {0, 0, 0}, "left": {0, 0, 0},
	} {
		dx, dy, ok := resizeKey(key)
		if dx != want[0] || dy != want[1] || ok != (want[2] == 1) {
			t.Errorf("%s: %d %d %v", key, dx, dy, ok)
		}
	}
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewRef{})
	// Outside the repeat window nothing happens.
	if _, ok := m.repeatResize("ctrl+left"); ok {
		t.Fatal("repeat outside the window")
	}
	m.resizeFocus(-3, 0)
	r := m.tab().root.ratio
	if r >= 0.5 {
		t.Fatalf("resize left: %v", r)
	}
	if _, ok := m.repeatResize("alt+left"); !ok || m.tab().root.ratio >= r {
		t.Fatal("repeat inside the window")
	}
	// Another key ends the window.
	if _, ok := m.repeatResize("x"); ok || !m.repeatUntil.IsZero() {
		t.Fatal("non-resize key should end the repeat window")
	}
	// A border that can't move reports nothing.
	if m.resizeFocus(0, 1) != nil {
		t.Fatal("vertical resize without a vertical split")
	}
}

func TestA1FocusLeafAndMoveFocus(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	first := m.tab().root.leaves()[0].id
	if m.focusLeaf(999) != nil {
		t.Fatal("focusLeaf of a missing leaf")
	}
	m.moveFocus(-1, 0)
	if m.tab().focus != first || m.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("moveFocus left: focus %d cursor %q", m.tab().focus, m.cursor)
	}
	if m.moveFocus(-1, 0) != nil || m.tab().focus != first {
		t.Fatal("moved past the edge")
	}
	// paneArea / mainOrigin reflect the focused leaf's inner rect.
	cols, rows := m.paneArea()
	x, y := m.mainOrigin()
	if x != m.sidebarW+1 || y != 2 || cols <= 0 || rows != m.height-statusHeight-1-2 {
		t.Fatalf("area %dx%d at %d,%d", cols, rows, x, y)
	}
	m.zoom = true
	cols, rows = m.paneArea()
	x, y = m.mainOrigin()
	if x != 0 || y != 0 || cols != m.width || rows != m.height-statusHeight {
		t.Fatalf("zoomed area %dx%d at %d,%d", cols, rows, x, y)
	}
	var zero Model
	if c, r := zero.paneArea(); c != 80 || r != 24 {
		t.Fatalf("no size yet: %dx%d", c, r)
	}
}

func TestA1RemoveRowFromPreviewAndActiveTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	m.gotoTab(3)
	m.dropPane(localMachine, "p4") // the active, last tab
	if len(m.tabs) != 3 || m.activeTab != 2 {
		t.Fatalf("after dropping the active last tab: %d tabs, active %d", len(m.tabs), m.activeTab)
	}
	m.gotoTab(0)
	m.dropPane(localMachine, "p1") // the active, first tab: the next takes its place
	if len(m.tabs) != 2 || m.activeTab != 0 || m.tabs[0].root.leaves()[0].view.PaneID != "p2" {
		t.Fatalf("after dropping the first: %d tabs, active %d", len(m.tabs), m.activeTab)
	}
	// A preview showing the row is emptied, not closed.
	m.preview = &tab{root: &layoutNode{leaf: m.newLeaf(viewRef{Row: paneNodeID(localMachine, "p9"), Kind: kindPane})}}
	m.removeRow(paneNodeID(localMachine, "p9"))
	if !m.preview.root.leaf.view.empty() {
		t.Fatal("preview kept the removed row")
	}
	if m.shownElsewhere(paneNodeID(localMachine, "p2"), nil) != true || m.shownElsewhere(paneNodeID(localMachine, "p2"), m.tabs[0].root.leaf) {
		t.Fatal("shownElsewhere")
	}
}

func TestA1NewTabBesideRunningPaneStartsShell(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.gotoTab(0)
	a1At(t, m, machineID(localMachine)) // cursor away; the tab stays active after gotoTab
	m.gotoTab(0)
	cmd := m.newTab(viewRef{})
	if len(m.tabs) != 2 || !m.tab().focused().pick {
		t.Fatalf("new tab: %d", len(m.tabs))
	}
	if m.cursor != paneNodeID(localMachine, "p2") {
		t.Fatalf("cursor should move to the pane the shell starts beside: %q", m.cursor)
	}
	if cmd == nil {
		t.Fatal("no command to start the shell")
	}
	// A new tab from the machine row has no home group.
	m2, _ := a1Fixture(t, false)
	m2.newTab(viewRef{})
	if m2.tab().home.level != scopeNone {
		t.Fatalf("home from machine row: %v", m2.tab().home)
	}
	_ = tea.Quit
}

func TestA1TabBarCloseButtonNeedsItsTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m)
	a1At(t, m, cliID(localMachine))
	_, hits := m.tabBar(5)
	for _, h := range hits {
		if h.tab == -2 {
			t.Fatalf("close button without its tab: %+v", hits)
		}
	}
}

// A branch lists the tabs showing changes, not the project's agents and
// terminals: selecting one used to put a zsh and an agent tab in the bar
// beside the branch's own.
func TestBranchListsOnlyChangesTabs(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1FourTabs(t, m) // p1 (agent) and p2 (terminal) are in project r1
	a1Open(t, m, branchNodeID(localMachine, "r1", "feat"))
	if m.previewing {
		t.Fatal("opening a branch should make it a tab")
	}
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))

	var kinds []string
	for _, i := range m.visibleTabs() {
		kinds = append(kinds, m.viewLabel(m.tabs[i].root.leaves()[0].view))
	}
	if len(kinds) != 1 || kinds[0] != "feat" {
		t.Fatalf("branch lists %v, want only its changes", kinds)
	}
	// Its Branches section and "… more" row behave the same.
	for _, row := range []string{sectionID(localMachine, "r1", "branches"), moreID(localMachine, "r1")} {
		if indexOfRow(m.rows, row) < 0 {
			continue
		}
		a1At(t, m, row)
		for _, i := range m.visibleTabs() {
			if v := m.tabs[i].root.leaves()[0].view; v.Kind == kindPane {
				t.Fatalf("%s lists pane %s", row, v.PaneID)
			}
		}
	}
	// The project itself still lists everything it holds, changes included.
	a1At(t, m, projectNodeID(localMachine, "r1"))
	seen := map[string]bool{}
	for _, i := range m.visibleTabs() {
		seen[m.viewLabel(m.tabs[i].root.leaves()[0].view)] = true
	}
	// The browsing tab follows the cursor, so it now shows the project page.
	if !seen["api"] || !seen["claude"] || !seen["zsh"] {
		t.Fatalf("project lists %v, want its panes and the browsing tab", seen)
	}
	if got := m.scopeName(tabScope{level: scopeProject, machine: localMachine, project: "r1", section: kindBranches}); got != "api · changes" {
		t.Fatalf("scope name %q", got)
	}
}
