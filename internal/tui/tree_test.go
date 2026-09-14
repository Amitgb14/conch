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
  p:box~r1
    p:box~r1/branches
    p:box~r1/terminals
      pane:box~p1
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
