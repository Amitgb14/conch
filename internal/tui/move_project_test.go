package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// projectMoveModel is moveModel with a second task worktree, fix, which
// no agent works in. Claude in the main checkout (p1) is the agent there.
func projectMoveModel(t *testing.T) (m *Model, src, dst *a1Peer, remote *machine) {
	t.Helper()
	m, src, dst, remote = moveModel(t)
	proj := &m.machines[0].projects[0]
	proj.Worktrees = append(proj.Worktrees, proto.WorktreeInfo{Path: "/src/api-fix", Branch: "fix"},
		proto.WorktreeInfo{Path: "/src/api-x", Detached: true}) // detached: not a task worktree
	return m, src, dst, remote
}

func countCalls(p *a1Peer, method, has string) int {
	n := 0
	for _, msg := range p.snapshot() {
		if msg.Method == method && strings.Contains(string(msg.Params), has) {
			n++
		}
	}
	return n
}

func TestMoveProjectRuns(t *testing.T) {
	m, src, dst, remote := projectMoveModel(t)
	if m.openMoveProject(localMachine, "r1"); menuLabels(m.overlay.(*menu)) != "1 busybox" {
		t.Fatalf("machines: %v %q", m.overlay, m.flash)
	}
	deliver(t, m, runItem(t, m, 0))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no confirmation: %v %q", m.overlay, m.flash)
	}
	text := strings.Join(d.text, "\n")
	for _, want := range []string{
		"Move the project api to busybox · api.",
		"Task worktrees, with their commits and all their uncommitted work: feat, fix.",
		"then closed here: claude (working), codex, claude.",
		"Agents in the main checkout continue in the project's checkout there",
		"saved conversations no agent has open, stay here",
		"The worktrees here are kept",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "zsh") || strings.Contains(text, "api-x") {
		t.Errorf("a terminal or a detached checkout is listed:\n%s", text)
	}
	if n := countCalls(src, proto.MethodWorktreeDescribe, ""); n != 2 {
		t.Fatalf("described %d worktrees", n)
	}

	m.overlay = nil // as the dialog does on enter
	last := deliver(t, m, d.submit(m, nil))
	if _, ok := last.(projectMainDoneMsg); !ok {
		t.Fatalf("ended with %#v; flash %q", last, m.flash)
	}
	// Each worktree, one after the other, into api there.
	src.waitMethod(t, proto.MethodWorktreePack, `"path":"/src/api-feat"`)
	src.waitMethod(t, proto.MethodWorktreePack, `"path":"/src/api-fix"`)
	if n := countCalls(dst, proto.MethodWorktreeUnpack, `"project_id":"r5"`); n != 2 {
		t.Fatalf("unpacked %d", n)
	}
	// The agent in the main checkout starts in the clone's own checkout.
	dst.waitMethod(t, proto.MethodPaneCreate, `"agent":"claude","cwd":"/srv/api"`)
	for _, id := range []string{"p1", "p5", "p6"} {
		src.waitMethod(t, proto.MethodPaneClose, `"id":"`+id+`"`)
	}
	if countCalls(src, proto.MethodPaneClose, `"p7"`) > 0 {
		t.Fatal("closed the terminal")
	}
	if m.flash != "moved api to busybox · 2 worktrees · 3 agents continue there" || m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if remote.paneIndex("q1") < 0 || m.focus != focusMain || m.overlay != nil {
		t.Fatalf("panes %+v overlay %v", remote.panes, m.overlay)
	}
}

func TestMoveProjectFailures(t *testing.T) {
	// The worktrees can't be rebuilt there: each is named, and the agents
	// in the main checkout still continue.
	m, src, dst, _ := projectMoveModel(t)
	dst.setError(proto.MethodWorktreeUnpack, "branch is already checked out")
	m.openMoveProject(localMachine, "r1")
	deliver(t, m, runItem(t, m, 0))
	deliver(t, m, m.overlay.(*dialog).submit(m, nil))
	n, ok := m.overlay.(*dialog)
	if !ok || !n.notice {
		t.Fatalf("no notice: %v %q", m.overlay, m.flash)
	}
	text := strings.Join(n.text, "\n")
	for _, want := range []string{"  feat: rebuild the worktree: branch is already checked out", "  fix: rebuild the worktree"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(m.flash, "1 agent continues there") || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if countCalls(src, proto.MethodPaneClose, `"p5"`) > 0 || countCalls(src, proto.MethodPaneClose, `"p1"`) != 1 {
		t.Fatal("closed the wrong agents")
	}

	// No worktree can be described: said before anything starts, and the
	// main checkout's agents are still offered.
	m, src, _, _ = projectMoveModel(t)
	src.setError(proto.MethodWorktreeDescribe, "has unresolved conflicts")
	m.openMoveProject(localMachine, "r1")
	deliver(t, m, runItem(t, m, 0))
	text = strings.Join(m.overlay.(*dialog).text, "\n")
	if !strings.Contains(text, "Can't move, so they stay here: feat (has unresolved conflicts); fix (has unresolved conflicts).") ||
		!strings.Contains(text, "No task worktree to move.") || !strings.Contains(text, "then closed here: claude.") || strings.Contains(text, "codex") {
		t.Fatalf("text:\n%s", text)
	}

	// Guards.
	for name, c := range map[string]struct {
		setup func(m *Model)
		pid   string
		want  string
	}{
		"not git":    {func(m *Model) { m.machines[0].projects[0].Git = false }, "r1", "select a git project"},
		"no project": {nil, "r404", "select a git project"},
		"offline":    {func(m *Model) { m.machines[0].c = nil }, "r1", "local is"},
		"old server": {func(m *Model) { m.machines[0].c = a2Client("session.v1") }, "r1", "predates moving worktrees"},
		"nothing": {func(m *Model) {
			m.machines[0].projects[0].Worktrees = m.machines[0].projects[0].Worktrees[:1]
			m.machines[0].panes = nil
		}, "r1", "no task worktree and no agent"},
	} {
		m, _, _, _ := projectMoveModel(t)
		if c.setup != nil {
			c.setup(m)
		}
		m.flash = ""
		if cmd := m.openMoveProject(localMachine, c.pid); cmd != nil || m.overlay != nil || !strings.Contains(m.flash, c.want) {
			t.Errorf("%s: flash %q overlay %v", name, m.flash, m.overlay)
		}
	}
	// An old server on the machine picked.
	m, _, _, remote := projectMoveModel(t)
	remote.c = a2Client("session.v1")
	m.openMoveProject(localMachine, "r1")
	if cmd := runItem(t, m, 0); cmd != nil || !strings.Contains(m.flash, "the server on busybox predates moving worktrees") {
		t.Fatalf("old target: %q", m.flash)
	}
	// Only agents in the main checkout: that is still a move.
	m, _, _, _ = projectMoveModel(t)
	m.machines[0].projects[0].Worktrees = m.machines[0].projects[0].Worktrees[:1]
	if m.openMoveProject(localMachine, "r1"); m.overlay == nil {
		t.Fatalf("main agents only: %q", m.flash)
	}
}

func TestMoveProjectToNewSandbox(t *testing.T) {
	m, _, _, _ := projectMoveModel(t)
	m.machines = m.machines[:1]
	moveSandboxes(t, &sbProvider{created: sandbox.Sandbox{ID: "sb7"}})
	stubSetUp(t)
	m.openMoveProject(localMachine, "r1")
	deliver(t, m, runItem(t, m, 0))
	d := m.overlay.(*dialog)
	if text := strings.Join(d.text, "\n"); !strings.Contains(text, "Move the project api to a new Daytona sandbox.") ||
		!strings.Contains(text, "clones acme/api into ~/api first") {
		t.Fatalf("text:\n%s", text)
	}
	var added machineAddedMsg
	for _, msg := range a2Run(d.submit(m, nil)) {
		if a, ok := msg.(machineAddedMsg); ok {
			added = a
		}
	}
	if added.move == nil {
		t.Fatalf("no sandbox; flash %q", m.flash)
	}
	next, _ := m.Update(added)
	*m = next.(Model)
	mach := m.machine(added.m.ID)
	rc, dst := a1FakeClient(t, moveCaps...)
	mach.c, mach.state = rc, stateOnline
	dst.setResult(proto.MethodProjectClone, proto.ProjectInfo{ID: "r7", Name: "api", Path: "/home/daytona/api", Git: true})
	dst.setResult(proto.MethodWorktreeHave, proto.WorktreeHaveResult{})
	dst.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: "/up/x.tar.gz"})
	dst.setResult(proto.MethodWorktreeUnpack, proto.WorktreeUnpackResult{Path: "/home/daytona/api.worktrees/feat", Branch: "feat"})
	dst.setResult(proto.MethodSessionShare, proto.SessionShareResult{Pane: proto.PaneInfo{ID: "q1", Name: "claude", State: proto.PaneRunning}})
	dst.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "q2", Name: "codex", State: proto.PaneRunning})
	next, cmd := m.Update(projectsMsg{machine: mach.id, gen: mach.gen})
	*m = next.(Model)
	if _, ok := deliver(t, m, cmd).(projectMainDoneMsg); !ok {
		t.Fatalf("didn't finish; flash %q", m.flash)
	}
	if n := countCalls(dst, proto.MethodWorktreeUnpack, `"project_id":"r7"`); n != 2 {
		t.Fatalf("unpacked %d", n)
	}
	dst.waitMethod(t, proto.MethodPaneCreate, `"cwd":"/home/daytona/api"`)
	if !strings.HasPrefix(m.flash, "moved api to "+mach.label+" · 2 worktrees") {
		t.Fatalf("flash %q", m.flash)
	}
}

// T is in the status bar wherever it does something, and nowhere else.
func TestMoveStatusHints(t *testing.T) {
	m, _, _, _ := projectMoveModel(t)
	m.focus = focusSidebar
	hints := func(r row) string {
		r.id = "x"
		m.rows, m.cursor = []row{r}, "x"
		_, items := m.statusHints()
		var out []string
		for _, it := range items {
			out = append(out, ansi.Strip(it.text))
		}
		return strings.Join(out, "|")
	}
	for name, c := range map[string]struct {
		r    row
		want bool
	}{
		"task branch":     {row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}, true},
		"base branch":     {row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "main"}, false},
		"agent in feat":   {row{kind: kindPane, machine: localMachine, paneID: "p5"}, true},
		"agent in main":   {row{kind: kindPane, machine: localMachine, paneID: "p1"}, false},
		"terminal":        {row{kind: kindPane, machine: localMachine, paneID: "p7"}, false},
		"project":         {row{kind: kindProject, machine: localMachine, projectID: "r1"}, true},
		"unknown project": {row{kind: kindProject, machine: localMachine, projectID: "r404"}, false},
	} {
		got := hints(c.r)
		if strings.Contains(got, "T move") != c.want {
			t.Errorf("%s: %s", name, got)
		}
		// Second of the row's own, after "! waiting" when an agent waits.
		if c.want && !slices.Contains(strings.Split(got, "|")[:3], "T move") {
			t.Errorf("%s: T move isn't among the first: %s", name, got)
		}
	}
	// A bar of 120 columns keeps the first few hints only; T is among them
	// on every row it is for. Found on a real run, where it was last but
	// the menu and so was cut on branch and agent rows.
	m.width, m.height = 120, 40
	for name, r := range map[string]row{
		"task branch":   {kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"},
		"agent in feat": {kind: kindPane, machine: localMachine, paneID: "p5"},
		"project":       {kind: kindProject, machine: localMachine, projectID: "r1"},
	} {
		r.id = "x"
		m.rows, m.cursor = []row{r}, "x"
		bar := ansi.Strip(m.statusBar())
		if !strings.Contains(bar, "T move") {
			t.Errorf("%s at 120 columns: %s", name, bar)
		}
		if w := ansi.StringWidth(bar); w > 120 {
			t.Errorf("%s: bar is %d wide", name, w)
		}
	}
	// An old server: no hint anywhere.
	m.machines[0].c = a2Client("session.v1")
	if got := hints(row{kind: kindProject, machine: localMachine, projectID: "r1"}); strings.Contains(got, "T move") {
		t.Errorf("old server: %s", got)
	}

	// The project's menu offers it beside the clean-up.
	m, _, _, _ = projectMoveModel(t)
	got := a2MenuLabels(newRowMenu(*m, row{kind: kindProject, machine: localMachine, projectID: "r1"}, 0, 0))
	if !strings.Contains(got, "W Clean up worktrees… | T Move the whole project to another machine or a sandbox… | R Refresh") {
		t.Fatalf("project menu: %s", got)
	}
}
