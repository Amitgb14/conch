package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

var treeNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func sampleInput() treeInput {
	branches := []proto.BranchInfo{
		{Name: "main", Committed: treeNow.Add(-time.Hour), Worktree: "/src/api"},
		{Name: "feat/login", Committed: treeNow.Add(-2 * time.Hour), Worktree: "/src/api.worktrees/feat-login"},
		{Name: "fix/flaky", Committed: treeNow.Add(-24 * time.Hour)},
	}
	for i := 0; i < 20; i++ {
		branches = append(branches, proto.BranchInfo{Name: fmt.Sprintf("old/%02d", i), Committed: treeNow.Add(-60 * 24 * time.Hour)})
	}
	return treeInput{
		machines: []treeMachine{{
			id: localMachine,
			projects: []proto.ProjectInfo{
				{ID: "r1", Name: "api", Git: true, Base: "main", Branches: branches, Worktrees: []proto.WorktreeInfo{
					{Path: "/src/api", Branch: "main", Main: true},
					{Path: "/src/api.worktrees/feat-login", Branch: "feat/login"},
				}},
				{ID: "r2", Name: "web", Git: true, Base: "main"},
			},
			panes: []proto.PaneInfo{
				{ID: "p1", Name: "claude", ProjectID: "r1", Branch: "feat/login", Title: "Login form", State: proto.PaneRunning,
					Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}},
				{ID: "p2", Name: "zsh", ProjectID: "r1", Branch: "main", State: proto.PaneRunning},
				{ID: "p3", Name: "zsh", State: proto.PaneRunning}, // outside any project
			},
			agents: map[string]bool{},
		}},
		expanded: map[string]bool{},
		showAll:  map[string]bool{},
		now:      treeNow,
	}
}

func render(rows []row) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Repeat("  ", r.depth) + r.id + "\n")
	}
	return b.String()
}

func TestBuildTreeDefault(t *testing.T) {
	got := render(buildTree(sampleInput()))
	want := `m:local
  m:local/workspace
    p:r1
      p:r1/branches
        b:r1:main
        b:r1:feat/login
        b:r1:fix/flaky
        more:r1
      p:r1/agents
        pane:p1
      p:r1/terminals
        pane:p2
      p:r1/files
    p:r2
  m:local/cli
    m:local/terminals
      pane:p3
`
	if got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
	rows := buildTree(sampleInput())
	if r := rows[indexOfRow(rows, "more:r1")]; r.count != 20 {
		t.Fatalf("more count %d, want 20", r.count)
	}
	if r := rows[indexOfRow(rows, "m:local/workspace")]; r.count != 2 || r.kind != kindWorkspace || !r.expandable() {
		t.Fatalf("workspace row: %+v", r)
	}
	// Projects sit under Workspace, loose panes under CLI.
	if parentID(rows, indexOfRow(rows, "p:r2")) != "m:local/workspace" || parentID(rows, indexOfRow(rows, "m:local/cli")) != "m:local" {
		t.Fatal("parents")
	}
}

func TestBuildTreeWorkspace(t *testing.T) {
	// Folded: projects hidden, CLI still listed.
	in := sampleInput()
	in.expanded["m:local/workspace"] = false
	got := render(buildTree(in))
	want := `m:local
  m:local/workspace
  m:local/cli
    m:local/terminals
      pane:p3
`
	if got != want {
		t.Fatalf("folded workspace:\n%s\nwant:\n%s", got, want)
	}
	// No projects: no Workspace row.
	in = sampleInput()
	in.machines[0].projects = nil
	rows := buildTree(in)
	if indexOfRow(rows, "m:local/workspace") >= 0 || indexOfRow(rows, "m:local/cli") < 0 {
		t.Fatalf("without projects:\n%s", render(rows))
	}
	// A filter matching only a loose pane doesn't list Workspace.
	in = sampleInput()
	in.filter = "p3"
	for _, r := range buildTree(in) {
		if r.kind == kindWorkspace {
			t.Fatalf("workspace listed for a CLI match:\n%s", render(buildTree(in)))
		}
	}
}

func TestBuildTreeExpansionAndShowAll(t *testing.T) {
	in := sampleInput()
	in.expanded["p:r1/branches"] = false
	in.expanded["p:r2"] = true
	rows := buildTree(in)
	if indexOfRow(rows, "b:r1:main") >= 0 {
		t.Fatal("collapsed section still shows branches")
	}

	in = sampleInput()
	in.showAll["r1"] = true
	rows = buildTree(in)
	if indexOfRow(rows, "b:r1:old/19") < 0 || indexOfRow(rows, "more:r1") >= 0 {
		t.Fatal("show all did not list every branch")
	}
}

func TestBuildTreeFilter(t *testing.T) {
	in := sampleInput()
	in.filter = "login"
	got := render(buildTree(in))
	want := `m:local
  m:local/workspace
    p:r1
      p:r1/branches
        b:r1:feat/login
      p:r1/agents
        pane:p1
`
	if got != want {
		t.Fatalf("filtered tree:\n%s\nwant:\n%s", got, want)
	}
}

func TestExitedAgentStaysUnderAgents(t *testing.T) {
	in := sampleInput()
	in.machines[0].panes[0].Agent = nil
	in.machines[0].panes[0].State = proto.PaneExited
	in.machines[0].agents["p1"] = true
	rows := buildTree(in)
	if parentID(rows, indexOfRow(rows, "pane:p1")) != "p:r1/agents" {
		t.Fatalf("exited agent moved:\n%s", render(rows))
	}
}

func TestBuildTreeRemoteMachine(t *testing.T) {
	in := sampleInput()
	in.machines = append(in.machines, treeMachine{
		id:       "box",
		projects: []proto.ProjectInfo{{ID: "r1", Name: "api", Git: true, Base: "main"}}, // same ID as local, different machine
		panes:    []proto.PaneInfo{{ID: "p1", Name: "bash", ProjectID: "r1", State: proto.PaneRunning}},
		agents:   map[string]bool{},
	})
	in.expanded["m:local"] = false
	got := render(buildTree(in))
	want := `m:local
m:box
  m:box/workspace
    p:box~r1
      p:box~r1/branches
      p:box~r1/terminals
        pane:box~p1
      p:box~r1/files
`
	if got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
	rows := buildTree(in)
	if r := rows[indexOfRow(rows, "pane:box~p1")]; r.machine != "box" || r.paneID != "p1" {
		t.Fatalf("remote pane row: %+v", r)
	}
}

func TestLooseAgentsGetTheirOwnSection(t *testing.T) {
	in := sampleInput()
	in.machines[0].panes = append(in.machines[0].panes, proto.PaneInfo{ID: "p9", Name: "claude", State: proto.PaneRunning,
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}})
	rows := buildTree(in)
	if parentID(rows, indexOfRow(rows, "pane:p9")) != "m:local/agents" || parentID(rows, indexOfRow(rows, "pane:p3")) != "m:local/terminals" {
		t.Fatalf("loose panes:\n%s", render(rows))
	}
	if parentID(rows, indexOfRow(rows, "m:local/agents")) != "m:local/cli" {
		t.Fatalf("machine sections not under CLI:\n%s", render(rows))
	}
}

// TestBuildTreeGroupedByTab: with [ui] tree_groups = "tabs" a project's
// panes are listed under the tab each is open in, and the panes open in no
// tab keep their own sections below — closing a tab hides nothing.
func TestBuildTreeGroupedByTab(t *testing.T) {
	in := sampleInput()
	in.tabs = []treeTab{{machine: localMachine, projectID: "r1", label: "Tab 1  claude", n: 1, panes: []string{"p1"}}}
	got := render(buildTree(in))
	want := `m:local
  m:local/workspace
    p:r1
      p:r1/tab/1
        pane:p1
      p:r1/branches
        b:r1:main
        b:r1:feat/login
        b:r1:fix/flaky
        more:r1
      p:r1/terminals
        pane:p2
      p:r1/files
    p:r2
  m:local/cli
    m:local/terminals
      pane:p3
`
	if got != want {
		t.Fatalf("grouped by tab:\n%s\nwant\n%s", got, want)
	}

	// A tab of both panes takes both, and the sections for them go.
	in.tabs = []treeTab{{machine: localMachine, projectID: "r1", label: "Tab 1  claude ⊞", n: 1, splits: true,
		panes: []string{"p1", "p2"}}}
	got = render(buildTree(in))
	for _, s := range []string{"p:r1/tab/1\n", "pane:p1\n", "pane:p2\n"} {
		if !strings.Contains(got, s) {
			t.Fatalf("a tab of two panes:\n%s", got)
		}
	}
	if strings.Contains(got, "p:r1/agents") || strings.Contains(got, "p:r1/terminals") {
		t.Fatalf("a section stayed for a pane that is in a tab:\n%s", got)
	}

	// Panes the tab names that conch does not know are skipped, and a tab
	// left with none of its own is not listed at all.
	in.tabs = []treeTab{{machine: localMachine, projectID: "r1", label: "Tab 1  gone", n: 1, panes: []string{"p9"}}}
	got = render(buildTree(in))
	if strings.Contains(got, "p:r1/tab/1") {
		t.Fatalf("a tab of panes that are gone was listed:\n%s", got)
	}
	if !strings.Contains(got, "p:r1/agents") || !strings.Contains(got, "p:r1/terminals") {
		t.Fatalf("the sections did not come back:\n%s", got)
	}

	// A tab of another machine's or another project's panes is not this
	// project's business.
	in.tabs = []treeTab{{machine: "box", projectID: "r1", label: "Tab 1  elsewhere", n: 1, panes: []string{"p1"}}}
	if got := render(buildTree(in)); strings.Contains(got, "p:r1/tab/1") {
		t.Fatalf("another machine's tab was listed:\n%s", got)
	}
	in.tabs = []treeTab{{machine: localMachine, projectID: "r2", label: "Tab 1  other", n: 1, panes: []string{"p1"}}}
	if got := render(buildTree(in)); strings.Contains(got, "p:r1/tab/1") {
		t.Fatalf("another project's tab took r1's pane:\n%s", got)
	}

	// Folded, the tab is listed without its panes.
	in.tabs = []treeTab{{machine: localMachine, projectID: "r1", label: "Tab 1  claude", n: 1, panes: []string{"p1"}}}
	in.expanded = map[string]bool{"p:r1/tab/1": false}
	got = render(buildTree(in))
	if !strings.Contains(got, "p:r1/tab/1") || strings.Contains(got, "pane:p1\n") {
		t.Fatalf("folded:\n%s", got)
	}

	// With no tabs given — the setting off — the tree is exactly as it was.
	in.tabs, in.expanded = nil, map[string]bool{}
	if got, plain := render(buildTree(in)), render(buildTree(sampleInput())); got != plain {
		t.Fatalf("the setting off changed the tree:\n%s\nwant\n%s", got, plain)
	}
}

// TestBuildTreeFolders: groups of your own inside a pane section — the
// panes you did not put in one, then the folders with theirs, so nothing
// one step out from a folder's contents is listed under them.
func TestBuildTreeFolders(t *testing.T) {
	in := sampleInput()
	key := folderKey(localMachine, "r1", kindTerminals)
	// Folders are folded until opened, so what sits under an open one is
	// its own. These cases look inside, so they open them.
	eng := folderRowID(localMachine, "r1", kindTerminals, "eng")
	prod := folderRowID(localMachine, "r1", kindTerminals, "prod")
	boxes := folderRowID(localMachine, "", kindTerminals, "boxes")
	in.expanded = map[string]bool{eng: true, prod: true, boxes: true}
	in.folders = map[string][]savedFolder{key: {
		{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p2"}}},
		{Name: "prod"},
	}}
	got := render(buildTree(in))
	want := `m:local
  m:local/workspace
    p:r1
      p:r1/branches
        b:r1:main
        b:r1:feat/login
        b:r1:fix/flaky
        more:r1
      p:r1/agents
        pane:p1
      p:r1/terminals
        folder:r1/4/eng
          pane:p2
        folder:r1/4/prod
      p:r1/files
    p:r2
  m:local/cli
    m:local/terminals
      pane:p3
`
	if got != want {
		t.Fatalf("with folders:\n%s\nwant\n%s", got, want)
	}

	// A pane the folder names but conch does not have is simply not there,
	// and the folder stays — the pane may come back.
	in.folders = map[string][]savedFolder{key: {{Name: "eng", Members: []savedMember{{Name: "gone", ID: "p9"}}}}}
	got = render(buildTree(in))
	if !strings.Contains(got, "folder:r1/4/eng\n") {
		t.Fatalf("the folder went with its pane:\n%s", got)
	}
	if !strings.Contains(got, "      p:r1/terminals\n        folder:r1/4/eng\n        pane:p2\n") {
		t.Fatalf("the pane did not stay outside it:\n%s", got)
	}

	// A member with a name and no id holds nothing: the tree does not go
	// looking by name, which settleFolders does once when a machine's
	// panes arrive (folders.go).
	in.folders = map[string][]savedFolder{key: {{Name: "eng", Members: []savedMember{{Name: "zsh"}}}}}
	if got := render(buildTree(in)); strings.Contains(got, "folder:r1/4/eng\n          pane:p2\n") {
		t.Fatalf("the tree found a pane by name:\n%s", got)
	}

	// By id alone, for a pane that never had a name.
	in.folders = map[string][]savedFolder{key: {{Name: "eng", Members: []savedMember{{ID: "p2"}}}}}
	if got := render(buildTree(in)); !strings.Contains(got, "folder:r1/4/eng\n          pane:p2\n") {
		t.Fatalf("a pane held by id was not found:\n%s", got)
	}

	// Two folders naming the same pane: the first keeps it, so it is in one
	// place rather than two.
	in.folders = map[string][]savedFolder{key: {
		{Name: "eng", Members: []savedMember{{ID: "p2"}}},
		{Name: "prod", Members: []savedMember{{ID: "p2"}}},
	}}
	got = render(buildTree(in))
	if strings.Count(got, "pane:p2") != 1 {
		t.Fatalf("the pane is in two places:\n%s", got)
	}

	// A machine's own section takes folders too.
	in.folders = map[string][]savedFolder{
		folderKey(localMachine, "", kindTerminals): {{Name: "boxes", Members: []savedMember{{ID: "p3"}}}},
	}
	// scoped() leaves this computer's ids bare, so its own sections key on
	// the empty project rather than on "local".
	if got := render(buildTree(in)); !strings.Contains(got, "    m:local/terminals\n      folder:/4/boxes\n        pane:p3\n") {
		t.Fatalf("a machine's own folder:\n%s", got)
	}

	// Folders come before the panes in none of them, at project and machine
	// level alike, as the file explorer lists folders before files. A pane
	// listed after an open folder sits one step out from what is in it,
	// which is the tree saying it is not in it; its own mark says so too.
	mixed := sampleInput()
	mixed.expanded = map[string]bool{eng: true, boxes: true}
	mixed.machines[0].panes = append(mixed.machines[0].panes,
		proto.PaneInfo{ID: "p4", Name: "b", ProjectID: "r1", Branch: "main", State: proto.PaneRunning},
		proto.PaneInfo{ID: "p5", Name: "c", State: proto.PaneRunning})
	mixed.folders = map[string][]savedFolder{
		key: {{Name: "eng", Members: []savedMember{{ID: "p2"}}}},
		folderKey(localMachine, "", kindTerminals): {{Name: "boxes", Members: []savedMember{{ID: "p3"}}}},
	}
	got = render(buildTree(mixed))
	for _, want := range []string{
		"      p:r1/terminals\n        folder:r1/4/eng\n          pane:p2\n        pane:p4\n      p:r1/files\n",
		"    m:local/terminals\n      folder:/4/boxes\n        pane:p3\n      pane:p5\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("folders not before the panes in none, want\n%s\nin\n%s", want, got)
		}
	}

	// Folded is the default — nothing said about it — and the folder is
	// listed without what is in it.
	in.folders = map[string][]savedFolder{key: {{Name: "eng", Members: []savedMember{{ID: "p2"}}}}}
	in.expanded = map[string]bool{}
	got = render(buildTree(in))
	if !strings.Contains(got, "folder:r1/4/eng") || strings.Contains(got, "          pane:p2") {
		t.Fatalf("folded:\n%s", got)
	}

	// No folders at all — state from a conch that never had them — and the
	// tree is exactly as it was.
	in.folders, in.expanded = nil, map[string]bool{}
	if got, plain := render(buildTree(in)), render(buildTree(sampleInput())); got != plain {
		t.Fatalf("no folders changed the tree:\n%s\nwant\n%s", got, plain)
	}
}

// TestBuildTreeNestsHelpers: a pane started by an agent is listed under it,
// so who is working for whom is the shape of the tree and not a chip on
// every row.
func TestBuildTreeNestsHelpers(t *testing.T) {
	in := sampleInput()
	ps := &in.machines[0].panes
	*ps = append(*ps,
		proto.PaneInfo{ID: "p4", Name: "reviewer", ProjectID: "r1", Branch: "review", State: proto.PaneRunning,
			CreatedBy: "p1", Agent: &proto.AgentStatus{Name: "claude"}},
		proto.PaneInfo{ID: "p5", Name: "tester", ProjectID: "r1", Branch: "test", State: proto.PaneRunning,
			CreatedBy: "p1", Agent: &proto.AgentStatus{Name: "codex"}},
		proto.PaneInfo{ID: "p6", Name: "deep", ProjectID: "r1", State: proto.PaneRunning,
			CreatedBy: "p4", Agent: &proto.AgentStatus{Name: "claude"}})
	got := render(buildTree(in))
	want := "      p:r1/agents\n        pane:p1\n          pane:p4\n            pane:p6\n          pane:p5\n"
	if !strings.Contains(got, want) {
		t.Fatalf("not nested:\n%s\nwant\n%s", got, want)
	}

	// Folded, the agent keeps its helpers to itself.
	in.expanded = map[string]bool{paneNodeID(localMachine, "p1"): false}
	got = render(buildTree(in))
	if strings.Contains(got, "pane:p4") || !strings.Contains(got, "pane:p1") {
		t.Fatalf("folded:\n%s", got)
	}
	in.expanded = map[string]bool{}

	// A creator in another section is not a parent here: the helper is
	// listed where it belongs, as it was before any of this.
	in.machines[0].panes[len(in.machines[0].panes)-3].CreatedBy = "p2" // a terminal
	got = render(buildTree(in))
	if !strings.Contains(got, "      p:r1/agents\n        pane:p1\n          pane:p5\n") {
		t.Fatalf("the other helper moved:\n%s", got)
	}
	if !strings.Contains(got, "        pane:p4\n") {
		t.Fatalf("a helper of another section went missing:\n%s", got)
	}

	// A pane that claims to have started itself, and a pair that claim each
	// other, are still listed — once each.
	in.machines[0].panes[len(in.machines[0].panes)-3].CreatedBy = "p4"
	in.machines[0].panes[len(in.machines[0].panes)-2].CreatedBy = "p6"
	in.machines[0].panes[len(in.machines[0].panes)-1].CreatedBy = "p5"
	got = render(buildTree(in))
	for _, id := range []string{"pane:p4", "pane:p5", "pane:p6"} {
		if n := strings.Count(got, id+"\n"); n != 1 {
			t.Fatalf("%s listed %d times:\n%s", id, n, got)
		}
	}
}
