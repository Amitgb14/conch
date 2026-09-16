package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/proto"
)

// a1Sized applies a window size as the program does (which clamps the
// sidebar) and returns the model.
func a1Sized(t *testing.T, m *Model, w, h int) *Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	nm := next.(Model)
	return &nm
}

// a1Variants builds models in different on-screen states.
func a1Variants(t *testing.T) map[string]*Model {
	t.Helper()
	out := map[string]*Model{}
	m, _ := a1Fixture(t, false)
	out["tree"] = m

	m, _ = a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.split(splitDown, viewRef{})
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: []string{"hello world", "second line"}, Offset: 2, History: 9}
	m.tab().sync = true
	m.focus = focusMain
	out["splits"] = m

	m, _ = a1Fixture(t, false)
	a1Open(t, m, projectNodeID(localMachine, "r1"))
	m.zoom = true
	out["zoomed project"] = m

	m, _ = a1Fixture(t, false)
	a1Open(t, m, branchNodeID(localMachine, "r1", "feat"))
	m.filtering, m.filter = true, "fe"
	out["branch filtering"] = m

	m, _ = a1Fixture(t, false)
	m.overlay = newConfirm("Really?", func(*Model) tea.Cmd { return nil })
	out["confirm overlay"] = m

	m, _ = a1Fixture(t, false)
	m.overlay = newHelp()
	out["help"] = m

	m, _ = a1Fixture(t, false)
	a1Open(t, m, projectNodeID(localMachine, "r1"))
	m.machines[0].state = stateOffline
	m.machines[0].err = "dial: refused"
	out["offline"] = m
	return out
}

func TestA1ViewTinySizesDoNotPanic(t *testing.T) {
	for name, base := range a1Variants(t) {
		for w := 1; w <= 20; w++ {
			for h := 2; h <= 5; h++ {
				m := a1Sized(t, base, w, h)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s at %dx%d: panic %v", name, w, h, r)
						}
					}()
					if lines := strings.Split(m.View(), "\n"); len(lines) != h {
						t.Fatalf("%s at %dx%d: %d lines", name, w, h, len(lines))
					}
				}()
			}
		}
	}
	var zero Model
	if zero.View() != "" {
		t.Fatal("a model without a size should render nothing")
	}
}

func TestA1ViewHeightOneDoesNotPanic(t *testing.T) {
	m, _ := a1Fixture(t, false)
	_ = a1Sized(t, m, 80, 1).View()
}

func TestA1ViewLinesFitWidth(t *testing.T) {
	for name, base := range a1Variants(t) {
		for _, w := range []int{30, 45, 64, 80, 100, 160} {
			if w < 34 && base.overlay != nil {
				continue // see TestA1ViewDialogFitsNarrowScreen
			}
			for _, h := range []int{2, 3, 5, 12, 40} {
				m := a1Sized(t, base, w, h)
				out := m.View()
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("%s at %dx%d: %d lines", name, w, h, len(lines))
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw > w {
						t.Fatalf("%s at %dx%d: line %d is %d wide: %q", name, w, h, i, lw, ansi.Strip(l))
					}
				}
			}
		}
	}
}

func TestA1ViewNarrowerThanSidebarFitsWidth(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for w := 1; w <= 20; w++ {
		for _, l := range strings.Split(a1Sized(t, m, w, 5).View(), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %d wide", w, ansi.StringWidth(l))
			}
		}
	}
}

func TestA1ViewDialogFitsNarrowScreen(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.overlay = newConfirm("Really?", func(*Model) tea.Cmd { return nil })
	for _, l := range strings.Split(a1Sized(t, m, 30, 12).View(), "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Fatalf("line %d wide", ansi.StringWidth(l))
		}
	}
}

func TestA1ViewContent(t *testing.T) {
	v := a1Variants(t)
	plain := func(name string) string { return ansi.Strip(a1Sized(t, v[name], 160, 40).View()) }

	tree := plain("tree")
	for _, want := range []string{"◆ conch", "MACHINES", "local", "api", "Branches", "feat", "#7✗", "↑2↓1", "Agents", "claude", "CLI", "codex", "TREE"} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree view lacks %q", want)
		}
	}
	splits := plain("splits")
	for _, want := range []string{"hello world", "↑ 2/9 lines back", "Pick something in the tree for this split", "ctrl+b x closes it", "SYNC"} {
		if !strings.Contains(splits, want) {
			t.Errorf("split view lacks %q", want)
		}
	}
	zoomed := plain("zoomed project")
	if strings.Contains(zoomed, "MACHINES") || !strings.Contains(zoomed, "Worktrees") || !strings.Contains(zoomed, "Pull requests  1 open  1 failing checks") {
		t.Errorf("zoomed project view:\n%s", zoomed)
	}
	if f := plain("branch filtering"); !strings.Contains(f, "/ fe█") || !strings.Contains(f, "FILTER") {
		t.Errorf("filter header missing:\n%s", f)
	}
	if c := plain("confirm overlay"); !strings.Contains(c, "Really?") {
		t.Errorf("confirm overlay missing")
	}
	if o := plain("offline"); !strings.Contains(o, "offline: dial: refused") {
		t.Errorf("offline machine page missing:\n%s", o)
	}
}

func TestA1EmptySidebarHints(t *testing.T) {
	m := &Model{width: 80, height: 20, sidebarW: 30, expanded: map[string]bool{}, showAll: map[string]bool{},
		frames: map[string]*proto.Frame{}, subscribed: map[string]bool{},
		machines: []*machine{newMachine(localMachine, "local", "")}}
	m.rebuild()
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "a  add a project") || !strings.Contains(out, "n  open a terminal") {
		t.Fatalf("empty sidebar hints missing:\n%s", out)
	}
}

func TestA1RowParts(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	parts := func(r row) (string, string, string) {
		g, _, l, _, right := m.rowParts(r)
		return g, l, ansi.Strip(right)
	}
	check := func(what string, r row, glyph, label, right string) {
		t.Helper()
		g, l, rt := parts(r)
		if g != glyph || l != label || rt != right {
			t.Errorf("%s: got (%q %q %q), want (%q %q %q)", what, g, l, rt, glyph, label, right)
		}
	}
	mr := row{id: "m:local", kind: kindMachine, machine: localMachine}
	check("online machine", mr, "●", "local", spinner[0]+"1")
	mach.warning = "old"
	check("outdated machine", mr, "●", "local", "outdated "+spinner[0]+"1")
	mach.state = stateConnecting
	m.spin = 3
	check("connecting", mr, spinner[3], "local", "connecting")
	mach.state = stateAttention
	check("attention", mr, "!", "local", "setup")
	mach.state = stateOffline
	check("offline", mr, "○", "local", "offline")
	check("unknown machine", row{kind: kindMachine, machine: "nope"}, "?", "nope", "")
	mach.state, mach.warning, m.spin = stateOnline, "", 0

	check("project", row{kind: kindProject, machine: localMachine, projectID: "r1"}, "◆", "api", "")
	mach.projects[0].Error = "boom"
	mach.panes[0].Agent.State = proto.AgentBlocked
	check("project error+waiting", row{kind: kindProject, machine: localMachine, projectID: "r1"}, "◆", "api", "git error ⚑1")
	mach.projects[0].Error = ""
	mach.panes[0].Agent.State = proto.AgentIdle
	check("missing project", row{kind: kindProject, machine: localMachine, projectID: "zz"}, "◆", "zz", "")
	check("branches", row{kind: kindBranches, count: 4}, "", "Branches", "4")
	check("agents", row{kind: kindAgents, count: 2}, "", "Agents", "2")
	check("terminals", row{kind: kindTerminals, count: 1}, "", "Terminals", "1")
	check("cli", row{kind: kindCLI, count: 2}, "❯", "CLI", "2")
	check("more", row{kind: kindMore, count: 9}, "", "… 9 more", "")
	sess := row{kind: kindSessions, machine: localMachine, projectID: "r1"}
	check("sessions unloaded", sess, "", "Sessions", "")
	m.sessions[sessionsKey(localMachine, "r1")] = &sessionsData{list: []proto.SessionInfo{{Interrupted: true}, {}}}
	check("sessions", sess, "", "Sessions", "⚠1 2")
	check("unknown kind", row{id: "weird", kind: nodeKind(99)}, "", "weird", "")

	// Panes.
	pr := func(id string) row { return row{kind: kindPane, machine: localMachine, paneID: id} }
	check("agent on a branch", pr("p1"), "○", "claude", "feat")
	check("working agent", pr("p4"), spinner[0], "codex", "working")
	check("terminal", pr("p3"), "›", "bash", "")
	check("missing pane", pr("zz"), "?", "zz", "")
	mach.panes[2].State, mach.panes[2].ExitCode = proto.PaneExited, 2
	check("failed pane", pr("p3"), "✗", "bash", "exit 2")
	mach.panes[2].ExitCode = 0
	check("exited pane", pr("p3"), "○", "bash", "exited")
	mach.panes[3].Agent.State = proto.AgentDone
	check("done agent", pr("p4"), "✓", "codex", "done")

	// Branches.
	br := func(b string) row { return row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: b} }
	check("main worktree", br("main"), "●", "main", "")
	check("linked worktree with PR", br("feat"), "◇", "feat", "○ +3 −1 #7✗ ↑2↓1")
	proj := &mach.projects[0]
	proj.Worktrees[1].Status = &proto.GitStatus{Files: 1, Conflicts: 2}
	proj.Branches[1].Gone = true
	check("conflicts, gone", br("feat"), "◇", "feat", "○ ⚠2 #7✗ gone")
	proj.Worktrees[1].Status = &proto.GitStatus{Files: 4}
	proj.Branches[1].Gone = false
	proj.Branches[1].PR = nil
	check("dirty without stats", br("feat"), "◇", "feat", "○ ~4 ↑2↓1")
	proj.Branches[0].Ahead, proj.Branches[0].Behind = 1, 3
	check("base ahead/behind remote", br("main"), "●", "main", "⇡1⇣3")
	check("plain branch", br("nobody"), "·", "nobody", "")
	check("branch of missing project", row{kind: kindBranch, machine: localMachine, projectID: "zz", branch: "x"}, "·", "x", "")

	// A pane on an offline machine is muted but still labelled.
	mach.state = stateOffline
	check("offline pane", pr("p1"), "○", "claude", "feat")
}

func TestA1RowLineSelection(t *testing.T) {
	m, _ := a1Fixture(t, false)
	r := m.rows[indexOfRow(m.rows, projectNodeID(localMachine, "r1"))]
	m.cursor = r.id
	for _, w := range []int{4, 10, 28} {
		if got := ansi.StringWidth(m.rowLine(r, w)); got != w {
			t.Errorf("selected row at %d: %d wide", w, got)
		}
		if got := ansi.StringWidth(m.rowLine(m.rows[0], w)); got != w {
			t.Errorf("unselected row at %d: %d wide", w, got)
		}
	}
	if !strings.Contains(ansi.Strip(m.rowLine(r, 28)), "▾ ◆ api") {
		t.Fatalf("open project: %q", ansi.Strip(m.rowLine(r, 28)))
	}
	m.expanded[r.id] = false
	m.rebuild()
	m.overlay = newHelp()
	if !strings.Contains(ansi.Strip(m.rowLine(r, 28)), "▸ ◆ api") {
		t.Fatalf("folded project: %q", ansi.Strip(m.rowLine(r, 28)))
	}
}

func TestA1PRBadgeAndSummary(t *testing.T) {
	for _, tc := range []struct {
		pr    proto.PRInfo
		badge string
	}{
		{proto.PRInfo{Number: 1, State: "MERGED"}, "#1"},
		{proto.PRInfo{Number: 2, State: "CLOSED"}, "#2×"},
		{proto.PRInfo{Number: 3, State: "OPEN", Draft: true, Checks: "pass"}, "#3"},
		{proto.PRInfo{Number: 4, State: "OPEN", Checks: "pass"}, "#4✓"},
		{proto.PRInfo{Number: 5, State: "OPEN", Checks: "fail"}, "#5✗"},
		{proto.PRInfo{Number: 6, State: "OPEN", Checks: "pending"}, "#6●"},
		{proto.PRInfo{Number: 7, State: "OPEN"}, "#7"},
	} {
		if got := ansi.Strip(prBadge(&tc.pr)); got != tc.badge {
			t.Errorf("badge %+v: %q", tc.pr, got)
		}
	}
	for _, tc := range []struct {
		pr   proto.PRInfo
		want string
	}{
		{proto.PRInfo{State: "OPEN", Checks: "pass", Passed: 3, Total: 3, Review: "APPROVED"}, "open · checks passing 3/3 · approved"},
		{proto.PRInfo{State: "OPEN", Draft: true, Checks: "fail", Passed: 1, Total: 3, Review: "CHANGES_REQUESTED"}, "draft · checks failing (1/3 passed) · changes requested"},
		{proto.PRInfo{State: "MERGED", Checks: "pending", Passed: 0, Total: 2, Review: "REVIEW_REQUIRED"}, "merged · checks running 0/2 · review required"},
		{proto.PRInfo{State: "CLOSED"}, "closed"},
	} {
		if got := ansi.Strip(prSummary(&tc.pr)); got != tc.want {
			t.Errorf("summary %+v: %q", tc.pr, got)
		}
	}
}

func TestA1LeafTitle(t *testing.T) {
	m, _ := a1Fixture(t, false)
	lt := func(v viewRef) string { return m.leafTitle(&leaf{view: v}) }
	p := func(id string) viewRef { return viewRef{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: id} }
	m.machines[0].panes[0].Agent.Tokens = &proto.Tokens{Context: 12000, ContextSize: 200000, Output: 1500, CostUSD: 0.005}
	m.brain = newBrainState()
	m.brain.summaries[paneKey(localMachine, "p1")] = &paneSummary{Summary: brain.Summary{Doing: "writing tests"}}
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{Offset: 4, History: 40}
	if got := lt(p("p1")); got != " claude · idle · feat · ctx 12k/200k · out 1.5k · <$0.01 · ✦ writing tests · ↑ 4/40 lines back " {
		t.Errorf("agent title %q", got)
	}
	m.machines[0].panes[2].State, m.machines[0].panes[2].ExitCode = proto.PaneExited, 3
	if got := lt(p("p3")); got != " bash · exited 3 " {
		t.Errorf("exited title %q", got)
	}
	if got := lt(p("zz")); got != " closed " {
		t.Errorf("closed title %q", got)
	}
	if got := lt(viewRef{Row: "b", Kind: kindBranch, Machine: localMachine, Branch: "feat"}); got != " changes · feat " {
		t.Errorf("branch title %q", got)
	}
	if got := lt(viewRef{Row: "s", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"}); got != " sessions · api " {
		t.Errorf("sessions title %q", got)
	}
	if got := lt(viewRef{Row: "s", Kind: kindSessions, Machine: localMachine, ProjectID: "zz"}); got != " local " {
		t.Errorf("sessions of a missing project %q", got)
	}
	if got := lt(viewRef{Row: "a", Kind: kindAgents, Machine: localMachine, ProjectID: "r1"}); got != " api " {
		t.Errorf("section title %q", got)
	}
	if got := lt(viewRef{Row: "m", Kind: kindMachine, Machine: "gone"}); got != " empty " {
		t.Errorf("unknown machine title %q", got)
	}
	// A remote machine's pane names the machine.
	box := newMachine("box", "buildbox", "me@box")
	box.panes = []proto.PaneInfo{{ID: "q1", Name: "sh", State: proto.PaneRunning, Branch: "dev"}}
	m.machines = append(m.machines, box)
	if got := lt(viewRef{Row: "x", Kind: kindPane, Machine: "box", PaneID: "q1"}); got != " buildbox · sh · dev " {
		t.Errorf("remote title %q", got)
	}
}

func TestA1LeafLines(t *testing.T) {
	m, _ := a1Fixture(t, false)
	text := func(l *leaf, focused bool) string {
		return ansi.Strip(strings.Join(m.leafLines(l, 60, 12, focused), "\n"))
	}
	p1 := &leaf{view: viewRef{Row: "pane:p1", Kind: kindPane, Machine: localMachine, PaneID: "p1"}}
	if got := text(p1, true); !strings.Contains(got, "connecting…") {
		t.Errorf("no frame: %q", got)
	}
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{Lines: []string{"abc def", "ghi"}}
	m.viewing = "p1"
	m.sel = &selection{paneID: "p1", ax: 0, ay: 0, bx: 2, by: 0}
	m.scrollMode, m.curX, m.curY = true, 1, 1
	lines := m.leafLines(p1, 60, 4, true)
	if len(lines) != 4 || ansi.Strip(lines[0]) != "abc def"+strings.Repeat(" ", 53) && ansi.Strip(lines[0])[:7] != "abc def" {
		t.Errorf("frame lines: %q", lines)
	}
	if lines[0] == "abc def" || lines[1] == "ghi" {
		t.Errorf("selection or cursor not highlighted: %q", lines)
	}
	if got := text(&leaf{view: viewRef{Row: "pane:zz", Kind: kindPane, Machine: localMachine, PaneID: "zz"}}, false); !strings.Contains(got, "This pane was closed") {
		t.Errorf("closed pane: %q", got)
	}
	if got := text(&leaf{}, false); !strings.Contains(got, "Pick something") {
		t.Errorf("empty leaf: %q", got)
	}
	if got := text(&leaf{view: viewRef{Row: "p", Kind: kindProject, Machine: "gone", ProjectID: "r1"}}, false); !strings.Contains(got, "This machine was removed") {
		t.Errorf("removed machine: %q", got)
	}
	branch := &leaf{view: viewRef{Row: "b", Kind: kindBranch, Machine: localMachine, ProjectID: "r1", Branch: "feat"},
		changes: &changesView{machine: localMachine, projectID: "r1", branch: "feat"}}
	if got := text(branch, false); !strings.Contains(got, "feat") {
		t.Errorf("branch: %q", got)
	}
	// A branch without its view yet and sessions without theirs fall back
	// to the machine page.
	if got := text(&leaf{view: viewRef{Row: "b", Kind: kindBranch, Machine: localMachine, Branch: "x"}}, false); !strings.Contains(got, "this computer") {
		t.Errorf("branch without view: %q", got)
	}
	sess := &leaf{view: viewRef{Row: "s", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"},
		sessions: &sessionsView{machine: localMachine, projectID: "r1"}}
	_ = text(sess, true)
	// Any non-machine view of an offline machine shows the machine page.
	m.machines[0].state = stateOffline
	if got := text(p1, false); !strings.Contains(got, "offline") {
		t.Errorf("offline pane: %q", got)
	}
}

func TestA1ProjectAndMachineLines(t *testing.T) {
	m, _ := a1Fixture(t, false)
	proj := m.machines[0].projects[0]
	proj.Error = "not a repo"
	proj.PRStatus = "gh missing"
	proj.Worktrees = append(proj.Worktrees, proto.WorktreeInfo{Path: "/src/api-x", Detached: true, Head: "abcdef123"})
	m.brain = newBrainState()
	m.brain.summaries[paneKey(localMachine, "p1")] = &paneSummary{Summary: brain.Summary{Needs: "a decision"}}
	got := ansi.Strip(strings.Join(m.projectLines(localMachine, proj, 80), "\n"))
	for _, want := range []string{"api", "/src/api · base main", "not a repo", "Agents  1", "claude", "✦ needs: a decision",
		"Worktrees  3", "(detached abcdef1)", "+3 −1 2 files", "clean", "Pull requests  1 open  1 failing checks  gh missing", "#7✗ Feature"} {
		if !strings.Contains(got, want) {
			t.Errorf("project lines lack %q:\n%s", want, got)
		}
	}
	m.machines[0].panes = nil
	proj.Git = false
	got = ansi.Strip(strings.Join(m.projectLines(localMachine, proj, 80), "\n"))
	if !strings.Contains(got, "none yet") || strings.Contains(got, "Worktrees") {
		t.Errorf("plain project:\n%s", got)
	}

	mach := newMachine("box", "buildbox", "me@box")
	mach.server = proto.HelloResult{Hostname: "box.lan", Platform: "linux/arm64", Build: "abc"}
	mach.state = stateOnline
	mach.warning = "server is old"
	mach.panes = []proto.PaneInfo{
		{ID: "a", Agent: &proto.AgentStatus{State: proto.AgentWorking, Tokens: &proto.Tokens{Input: 1000, CacheRead: 500, Output: 2_500_000, CostUSD: 1.5}}},
		{ID: "b", Agent: &proto.AgentStatus{State: proto.AgentBlocked}},
	}
	mach.agentList = []proto.AgentAvailability{{Name: "claude", Installed: true, Version: "1.0"}, {Name: "codex"}}
	lines := func() string { return ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n")) }
	got = lines()
	for _, want := range []string{"buildbox  ssh me@box", "box.lan · linux/arm64 · conch build abc", "0 projects · 2 panes · 1 working · 1 waiting",
		"usage of 1 running agent(s): in 1.5k · out 2.5M · $1.50 reported", "✓ Claude Code 1.0", "○ Codex", "server is old", "M  add a machine"} {
		if !strings.Contains(got, want) {
			t.Errorf("online machine lacks %q:\n%s", want, got)
		}
	}
	mach.state = stateConnecting
	if !strings.Contains(lines(), "connecting…") {
		t.Error("connecting machine")
	}
	mach.state, mach.err = stateAttention, "conch missing"
	if got := lines(); !strings.Contains(got, "conch missing") || !strings.Contains(got, "conch machine upgrade box") {
		t.Errorf("attention machine:\n%s", got)
	}
	mach.state, mach.err = stateOffline, "refused"
	if got := lines(); !strings.Contains(got, "offline: refused") || !strings.Contains(got, "showing 2 panes as last seen") || !strings.Contains(got, "R  reconnect now") {
		t.Errorf("offline remote:\n%s", got)
	}
	local := newMachine(localMachine, "local", "")
	local.state = stateOffline
	if got := ansi.Strip(strings.Join(m.machineLines(local, 100, 30), "\n")); !strings.Contains(got, "m → Start the server") || !strings.Contains(got, "this computer") {
		t.Errorf("offline local:\n%s", got)
	}
}

func TestA1LayoutHelpers(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{999, "999"}, {1000, "1.0k"}, {9999, "10.0k"}, {10000, "10k"}, {1_250_000, "1.2M"}} {
		if got := humanCount(tc.n); got != tc.want {
			t.Errorf("humanCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	if got := tokenSummary(&proto.Tokens{Context: 500, Output: 10}); got != "ctx 500 · out 10" {
		t.Errorf("tokenSummary %q", got)
	}
	if got := usd(12.345); got != "$12.35" {
		t.Errorf("usd %q", got)
	}
	if n, in, out, cost := usageTotals([]proto.PaneInfo{{Agent: &proto.AgentStatus{}}, {}}); n != 0 || in != 0 || out != 0 || cost != 0 {
		t.Error("usageTotals without tokens")
	}
	if got := exactly([]string{"a", "b", "c"}, 2); len(got) != 2 || got[1] != "b" {
		t.Errorf("exactly cut %q", got)
	}
	if got := exactly([]string{"a"}, 3); len(got) != 3 || got[2] != "" {
		t.Errorf("exactly pad %q", got)
	}
	for _, w := range []int{0, 1, 5, 12, 30} {
		if got := ansi.StringWidth(spread("left side label", "right detail", w)); got > max(w, 0) {
			t.Errorf("spread at %d: %d wide", w, got)
		}
	}
	if got := spread("ab", "cd", 10); got != "ab      cd" {
		t.Errorf("spread fits: %q", got)
	}
	if got := spread("a-long-left-name", "right", 12); ansi.StringWidth(got) != 12 || strings.Contains(got, "right") {
		t.Errorf("spread drops the detail first: %q", got)
	}
	if got := spread("name", "a-long-detail-text", 14); !strings.HasPrefix(got, "name ") || !strings.Contains(got, "…") || ansi.StringWidth(got) != 14 {
		t.Errorf("spread truncates the detail's start: %q", got)
	}
	c := centered(10, 5, "ab", "abcd")
	if len(c) != 5 || c[1] != "   ab" || c[2] != "   abcd" {
		t.Errorf("centered %q", c)
	}
	if got := centered(4, 1, "a", "b", "c"); len(got) != 1 || got[0] != " a" {
		t.Errorf("centered overflow %q", got)
	}
	s := splice("0123456789", "ab", 3, 10)
	if ansi.Strip(s) != "012ab56789" {
		t.Errorf("splice %q", ansi.Strip(s))
	}
	if got := ansi.Strip(splice("01", "x", 5, 8)); got != "01   x" {
		t.Errorf("splice past the end %q", got)
	}
	if got := joinRight("", "b") + joinRight("a", "") + joinRight("a", "b"); got != "baa b" {
		t.Errorf("joinRight %q", got)
	}
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	m.machines[0].panes[3].Agent.State = proto.AgentDone
	if m.inboxCount() != 2 {
		t.Errorf("inbox %d", m.inboxCount())
	}
	if got := ansi.Strip(m.attentionBadge("nope", "")); got != "" {
		t.Errorf("badge of unknown machine %q", got)
	}
	if _, _, ok := m.branchAgentGlyph("nope", "r1", "feat"); ok {
		t.Error("branchAgentGlyph unknown machine")
	}
	if g, _, ok := m.branchAgentGlyph(localMachine, "r1", "feat"); !ok || g != "!" {
		t.Errorf("branchAgentGlyph %q %v", g, ok)
	}
	_ = fmt.Sprint()
}

// Branches shows its own page: every branch, not only the ones the tree
// lists, with where it is checked out and how it stands against the base.
// It used to show the project's page, which lists worktrees instead.
func TestBranchesPage(t *testing.T) {
	m, _ := a1Fixture(t, false)
	proj := m.machines[0].projects[0]
	proj.Branches = append(proj.Branches, proto.BranchInfo{Name: "old/one", BaseAhead: 1, BaseBehind: 4, Committed: time.Now().Add(-72 * time.Hour)},
		proto.BranchInfo{Name: "gone/x", Gone: true})
	m.machines[0].projects[0] = proj

	out := a2Plain(m.branchesLines(localMachine, proj, 100))
	for _, want := range []string{"Branches", "4 in api", "base main", "● main", "◇ feat", "old/one", "↑1↓4", "gone", "#7", "enter a branch for its changes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q:\n%s", want, out)
		}
	}
	// The checked-out branches name their worktree; the others don't.
	if !strings.Contains(out, "/src/api-feat") || strings.Contains(out, "old/one  /") {
		t.Fatalf("worktrees:\n%s", out)
	}
	// Every line fits, at any width.
	for _, w := range []int{20, 40, 100} {
		for _, l := range m.branchesLines(localMachine, proj, w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q", w, ansi.Strip(l))
			}
		}
	}
	// A project without branches says so rather than showing an empty list.
	empty := proto.ProjectInfo{ID: "r9", Name: "plain", Path: "/src/plain", Base: "main"}
	if out := a2Plain(m.branchesLines(localMachine, empty, 60)); !strings.Contains(out, "no branches yet") {
		t.Fatalf("no branches:\n%s", out)
	}

	// The Branches row shows that page, with its own title.
	a1At(t, m, sectionID(localMachine, "r1", "branches"))
	l := m.tab().focused()
	if got := a2Plain(m.leafLines(l, 100, 20, false)); !strings.Contains(got, "Branches") || strings.Contains(got, "Pull requests") {
		t.Fatalf("Branches row shows:\n%s", got)
	}
	if got := m.leafTitle(l); got != " branches · api " {
		t.Fatalf("title %q", got)
	}
	// The project row still shows the project page.
	a1At(t, m, projectNodeID(localMachine, "r1"))
	if got := a2Plain(m.leafLines(m.tab().focused(), 100, 20, false)); !strings.Contains(got, "Worktrees") || !strings.Contains(got, "Pull requests") {
		t.Fatalf("project row shows:\n%s", got)
	}
}
