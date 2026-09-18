package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// a6Model is a project whose task was tried three times, with an agent
// still working on one of them.
func a6Model(t *testing.T) (*Model, *a1Peer) {
	t.Helper()
	m, peer := harvestModel(t, harvestCapability)
	proj := &m.machines[0].projects[0]
	base := "conch/fix-flaky-test"
	proj.Branches = append(proj.Branches,
		proto.BranchInfo{Name: base + "/claude", Worktree: "/src/api-a", BaseAhead: 2},
		proto.BranchInfo{Name: base + "/codex", Worktree: "/src/api-b", BaseAhead: 1},
		proto.BranchInfo{Name: base + "/claude-2", Worktree: "/src/api-c"})
	proj.Worktrees = append(proj.Worktrees,
		proto.WorktreeInfo{Path: "/src/api-a", Branch: base + "/claude"},
		proto.WorktreeInfo{Path: "/src/api-b", Branch: base + "/codex"},
		proto.WorktreeInfo{Path: "/src/api-c", Branch: base + "/claude-2"})
	m.machines[0].panes = append(m.machines[0].panes, proto.PaneInfo{
		ID: "pa", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: base + "/claude",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking, Tokens: &proto.Tokens{Output: 9000, CostUSD: 0.9}}})
	m.rebuild()
	return m, peer
}

func openedCompare(t *testing.T, m *Model, branch string) *compareView {
	t.Helper()
	cmd := m.openCompare(harvestTarget{machine: localMachine, projectID: "r1", branch: branch})
	v, ok := m.overlay.(*compareView)
	if !ok {
		t.Fatalf("no compare view: %T %q", m.overlay, m.flash)
	}
	for _, msg := range a2Run(cmd) {
		if cm, ok := msg.(compareMsg); ok {
			v.receive(cm)
		}
	}
	return v
}

func TestCompareOpens(t *testing.T) {
	m, _ := a6Model(t)
	// A branch that is not one of several attempts says so.
	for _, branch := range []string{"feat", "main", "conch/fix-flaky-test/claude/deeper"} {
		m.flash, m.overlay = "", nil
		if cmd := m.openCompare(harvestTarget{machine: localMachine, projectID: "r1", branch: branch}); cmd != nil || m.overlay != nil {
			t.Fatalf("%s opened a compare view", branch)
		}
		if !strings.Contains(m.flash, "not one of several attempts") {
			t.Fatalf("%s: %q", branch, m.flash)
		}
	}
	m.flash = ""
	if cmd := m.openCompare(harvestTarget{machine: localMachine, projectID: "nope", branch: "x/y"}); cmd != nil || !strings.Contains(m.flash, "git project") {
		t.Fatalf("unknown project: %q", m.flash)
	}

	v := openedCompare(t, m, "conch/fix-flaky-test/codex")
	if len(v.rows) != 3 || v.rows[v.sel].name != "codex" {
		t.Fatalf("rows %+v sel %d", v.rows, v.sel)
	}
	if names := []string{v.rows[0].name, v.rows[1].name, v.rows[2].name}; strings.Join(names, ",") != "claude,claude-2,codex" {
		t.Fatalf("order: %v", names)
	}
	// The branch menu offers it, and doesn't for a plain branch.
	got := a2MenuLabels(newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "conch/fix-flaky-test/codex"}, 0, 0))
	if !strings.Contains(got, "A Compare the 3 attempts at conch/fix-flaky-test…") {
		t.Fatalf("menu:\n%s", got)
	}
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}, 0, 0)); strings.Contains(got, "Compare") {
		t.Fatalf("plain branch menu:\n%s", got)
	}
}

func TestCompareShowsEachAttempt(t *testing.T) {
	m, peer := a6Model(t)
	peer.setResult(proto.MethodProjectChanges, proto.Changes{ProjectID: "r1", Base: "main",
		Files:   []proto.FileChange{{Path: "a.go", Added: 12, Deleted: 3}, {Path: "b.go", Added: 4}},
		Commits: []proto.CommitInfo{{Hash: "abc1234", Subject: "Fix it"}}})
	v := openedCompare(t, m, "conch/fix-flaky-test/claude")
	out := a2Plain(v.render(*m).lines)
	t.Log("\n" + out)
	for _, want := range []string{"Attempts at conch/fix-flaky-test", "claude", "codex", "claude-2",
		"2 files", "+16", "−3", "1 commit", "working", "$0.90", "M merge into the base", "D discard it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compare view lacks %q:\n%s", want, out)
		}
	}
	// An attempt with nothing, and one that failed to read.
	v.rows[1].changes = &proto.Changes{}
	v.rows[2].changes, v.rows[2].err = nil, "no such branch"
	out = a2Plain(v.render(*m).lines)
	if !strings.Contains(out, "nothing yet") || !strings.Contains(out, "no such branch") {
		t.Fatalf("empty and failed rows:\n%s", out)
	}
	// Every width keeps the box square.
	for _, w := range []int{40, 80, 160} {
		m.width = w
		b := v.render(*m)
		for _, l := range b.lines {
			if ansi.StringWidth(l) != b.width() {
				t.Fatalf("width %d: %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
}

func TestCompareKeys(t *testing.T) {
	m, peer := a6Model(t)
	m.width = 120
	v := openedCompare(t, m, "conch/fix-flaky-test/claude")

	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"down", 2}, {"up", 1}, {"k", 0}, {"up", 0}} {
		v.update(m, a2Key(step.key))
		if v.sel != step.want {
			t.Fatalf("%s: sel %d, want %d", step.key, v.sel, step.want)
		}
	}
	// enter shows that attempt's changes in the tree.
	closed, cmd := v.update(m, a2Key("enter"))
	if !closed || m.overlay != nil || m.cursor != branchNodeID(localMachine, "r1", "conch/fix-flaky-test/claude") {
		t.Fatalf("enter: overlay %T cursor %q", m.overlay, m.cursor)
	}
	a2Run(cmd)

	// M and D hand over to the harvest steps, which ask before doing anything.
	v = openedCompare(t, m, "conch/fix-flaky-test/codex")
	v.update(m, a2Key("M"))
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("M: %T %q", m.overlay, m.flash)
	}
	v = openedCompare(t, m, "conch/fix-flaky-test/codex")
	peer.setResult(proto.MethodBranchDiscard, proto.BranchDiscardResult{Worktree: "/src/api-b"})
	_, cmd = v.update(m, a2Key("D"))
	if _, ok := a2Run(cmd)[0].(discardPlanMsg); !ok {
		t.Fatal("D asks what discarding would lose first")
	}

	// x discards the others, and never one an agent is still working in.
	v = openedCompare(t, m, "conch/fix-flaky-test/claude")
	_, cmd = v.update(m, a2Key("x"))
	msgs := a2Run(cmd)
	plan, ok := msgs[0].(discardPlanMsg)
	if !ok || plan.target.branch == "conch/fix-flaky-test/claude" {
		t.Fatalf("x: %#v", msgs)
	}
	// Keeping the one attempt without an agent leaves nothing to discard:
	// the others are still running.
	v = openedCompare(t, m, "conch/fix-flaky-test/codex")
	v.rows = v.rows[:1] // only claude, which has an agent
	v.sel = 0
	if _, cmd := v.update(m, a2Key("x")); cmd != nil || !strings.Contains(v.err, "still have agents running") {
		t.Fatalf("x with nothing to discard: %q", v.err)
	}

	v = openedCompare(t, m, "conch/fix-flaky-test/codex")
	if _, cmd := v.update(m, a2Key("R")); cmd == nil {
		t.Fatal("R reloads")
	}
	if closed, _ := v.update(m, a2Key("esc")); !closed || m.overlay != nil {
		t.Fatal("esc closes")
	}
}

func TestCompareMouse(t *testing.T) {
	m, _ := a6Model(t)
	m.width, m.height = 120, 40
	v := openedCompare(t, m, "conch/fix-flaky-test/claude")
	b := v.render(*m)
	click := func(y int) tea.Cmd {
		return v.mouse(m, tea.MouseMsg{X: b.x + 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	}
	if click(b.y + 3 + 2); v.sel != 2 {
		t.Fatalf("click selected %d", v.sel)
	}
	if cmd := click(b.y + 3 + 2); cmd == nil {
		t.Fatal("a second click on the same row opens it")
	}
	if click(b.y + 1); v.sel != 2 {
		t.Fatal("a click on the heading moved the selection")
	}
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside closes it")
	}
}

func TestCompareRunsACommand(t *testing.T) {
	m, peer := a6Model(t)
	m.width, m.height = 120, 40
	v := openedCompare(t, m, "conch/fix-flaky-test/claude")

	// t asks for the command; an empty one runs nothing.
	v.update(m, a2Key("t"))
	d, ok := m.overlay.(*dialog)
	if !ok || d.fields[0].in.Placeholder == "" {
		t.Fatalf("no command dialog: %T", m.overlay)
	}
	if cmd := d.submit(m, []string{"  "}); cmd != nil || m.overlay != v || v.err != "nothing to run" {
		t.Fatalf("empty command: %q", v.err)
	}

	// A real one starts a terminal per attempt, in its own worktree.
	v.update(m, a2Key("t"))
	d = m.overlay.(*dialog)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "t1", Name: "test", State: proto.PaneRunning, ProjectID: "r1"})
	msgs := a2Run(d.submit(m, []string{"go test ./..."}))
	if m.overlay != v || v.command != "go test ./..." {
		t.Fatalf("after submitting: %T %q", m.overlay, v.command)
	}
	if len(msgs) != 3 {
		t.Fatalf("%d terminals for 3 attempts", len(msgs))
	}
	var dirs, names []string
	for _, msg := range peer.snapshot() {
		if msg.Method != proto.MethodPaneCreate {
			continue
		}
		var p proto.PaneCreateParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, p.Cwd)
		names = append(names, p.Name)
		if strings.Join(p.Command, " ") != "/bin/sh -lc go test ./..." {
			t.Fatalf("command: %v", p.Command)
		}
	}
	if strings.Join(dirs, ",") != "/src/api-a,/src/api-c,/src/api-b" {
		t.Fatalf("worktrees: %v", dirs)
	}
	if strings.Join(names, ",") != "test · claude,test · claude-2,test · codex" {
		t.Fatalf("names: %v", names)
	}

	// Each result follows its terminal: running, passed, failed.
	for _, msg := range msgs {
		next, _ := m.update(msg)
		*m = next.(Model)
	}
	v.runs["conch/fix-flaky-test/claude"] = testRun{pane: "t1"}
	v.runs["conch/fix-flaky-test/codex"] = testRun{pane: "t2"}
	v.runs["conch/fix-flaky-test/claude-2"] = testRun{err: "no worktree"}
	mach := m.machines[0]
	mach.panes = append(mach.panes,
		proto.PaneInfo{ID: "t1", State: proto.PaneRunning},
		proto.PaneInfo{ID: "t2", State: proto.PaneExited, ExitCode: 2})
	out := a2Plain(v.render(*m).lines)
	t.Log("\n" + out)
	for _, want := range []string{"running…", "failed (exit 2)", "no worktree", "t rerun go test ./...", "o its terminal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("results lack %q:\n%s", want, out)
		}
	}
	mach.panes[len(mach.panes)-1].ExitCode = 0
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "passed") {
		t.Fatalf("passed:\n%s", out)
	}
	// A terminal the user closed says so rather than claiming a result.
	mach.panes = mach.panes[:len(mach.panes)-1]
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "closed") {
		t.Fatalf("closed terminal:\n%s", out)
	}

	// o opens the selected attempt's terminal; without one it says so.
	v.sel = 0
	closed, cmd := v.update(m, a2Key("o"))
	if !closed || m.overlay != nil || m.cursor != paneNodeID(localMachine, "t1") {
		t.Fatalf("o: overlay %T cursor %q", m.overlay, m.cursor)
	}
	a2Run(cmd)
	v = openedCompare(t, m, "conch/fix-flaky-test/codex")
	if _, cmd := v.update(m, a2Key("o")); cmd != nil || !strings.Contains(v.err, "nothing has run here yet") {
		t.Fatalf("o without a run: %q", v.err)
	}

	// An offline machine starts nothing.
	offline, _ := a6Model(t)
	offline.machines[0].c = nil
	ov := openedCompare(t, offline, "conch/fix-flaky-test/claude")
	if cmd := ov.runCommand(offline, "go test ./..."); cmd != nil || !strings.Contains(ov.err, "local is") {
		t.Fatalf("offline: %q", ov.err)
	}
}
