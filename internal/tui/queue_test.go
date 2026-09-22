package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// a2QueueModel has one of everything the queue orders: an agent waiting, an
// older agent done, a branch with unpushed commits, a dirty branch, and
// several rows that must stay out — the base branch, a merged-and-pushed
// branch with an open PR, a clean branch, and a whole machine that is
// offline.
func a2QueueModel() (*Model, *queueView) {
	now := time.Now()
	m := a2Model()
	mach := m.machines[0]
	mach.projects = []proto.ProjectInfo{{ID: "r1", Name: "api", Path: "/src/api", Git: true, Base: "main",
		Worktrees: []proto.WorktreeInfo{
			{Path: "/src/api", Branch: "main", Main: true},
			{Path: "/src/api-feat", Branch: "feat"},
			{Path: "/src/api-wip", Branch: "wip", Status: &proto.GitStatus{Files: 3, Unstaged: 3}},
			{Path: "/src/api-old", Branch: "shipped"},
		},
		Branches: []proto.BranchInfo{
			{Name: "main"},
			{Name: "feat", BaseAhead: 2, Committed: now.Add(-3 * time.Hour)},
			{Name: "wip", Committed: now.Add(-30 * time.Hour)},
			{Name: "shipped", Committed: now.Add(-time.Hour),
				PR: &proto.PRInfo{Number: 7, State: "OPEN", URL: "https://example.invalid/pr/7"}},
			{Name: "quiet", Committed: now.Add(-time.Minute)},
			{Name: "answering", BaseAhead: 1, Committed: now.Add(-time.Minute)},
		}}}
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "answering",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Since: now.Add(-5 * time.Minute),
				Tokens: &proto.Tokens{CostUSD: 0.42}}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone, Since: now.Add(-2 * time.Hour)}},
		{ID: "p3", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "quiet",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking, Since: now.Add(-time.Minute)}},
		{ID: "p4", Name: "zsh", State: proto.PaneRunning},
	}
	off := newMachine("box", "box", "dev@box")
	off.state = stateOffline
	off.panes = []proto.PaneInfo{{ID: "q1", Name: "claude", State: proto.PaneRunning, ProjectID: "r2", Branch: "ghost",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Since: now.Add(-time.Hour)}}}
	m.machines = append(m.machines, off)
	return m, &queueView{}
}

func TestA2QueueItems(t *testing.T) {
	m, _ := a2QueueModel()
	items := m.queueItems()

	var got []string
	for _, it := range items {
		got = append(got, it.branch+":"+it.detail)
	}
	want := []string{
		"answering:waiting for an answer", // blocked first, whatever its age
		"feat:finished",                   // then done
		"wip:3 files uncommitted",         // then work nobody committed
	}
	if len(got) != len(want) {
		t.Fatalf("queue is %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d is %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
	// The branch of a waiting agent is not also listed for its commits, the
	// base branch and a clean branch never appear, an open PR with nothing
	// unpushed is somebody else's move, and an offline machine's cached
	// panes are not claimed to need anything.
	for _, absent := range []string{"main", "shipped", "quiet", "ghost"} {
		for _, it := range items {
			if it.branch == absent {
				t.Fatalf("%q should not be in the queue: %+v", absent, it)
			}
		}
	}
	if items[0].agent != "claude" || items[0].paneID != "p1" || items[0].cost.cost != 0.42 {
		t.Fatalf("waiting row lost its agent: %+v", items[0])
	}
}

func TestA2QueueOldestFirstWithinABand(t *testing.T) {
	m, _ := a2QueueModel()
	now := time.Now()
	mach := m.machines[0]
	// Two agents done: the one that has been sitting longer comes first.
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: now.Add(-time.Minute)}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "quiet",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone, Since: now.Add(-4 * time.Hour)}},
	}
	items := m.queueItems()
	if len(items) < 2 || items[0].branch != "quiet" || items[1].branch != "feat" {
		t.Fatalf("not oldest first: %+v", items)
	}
	// A failed agent says so rather than claiming it finished.
	mach.panes[0].Agent.Failed = true
	if it := m.queueItems()[1]; it.detail != "stopped with an error" {
		t.Fatalf("failed agent: %q", it.detail)
	}
}

// Old branches drop out: the queue is what to do now, not a history of
// every branch a repository ever had. A checked-out worktree stays,
// however old its last commit, because its uncommitted changes are still
// sitting there.
func TestA2QueueLeavesOldBranchesOut(t *testing.T) {
	m, _ := a2QueueModel()
	proj := &m.machines[0].projects[0]
	for i := range proj.Branches {
		if proj.Branches[i].Name == "feat" {
			proj.Branches[i].Committed = time.Now().Add(-40 * 24 * time.Hour)
		}
	}
	m.machines[0].panes = nil // no agent holds feat's place any more

	var got []string
	for _, it := range m.queueItems() {
		got = append(got, it.branch)
	}
	// feat is 40 days old with no worktree, so it goes. wip is 30 hours
	// old but checked out with changes, and answering was committed a
	// minute ago, so both stay.
	if strings.Join(got, ",") != "answering,wip" {
		t.Fatalf("queue is %q, want the recent branch and the checked-out one", got)
	}
	// A branch with no commit date at all (a fresh one) is not aged out.
	for i := range proj.Branches {
		if proj.Branches[i].Name == "feat" {
			proj.Branches[i].Committed = time.Time{}
		}
	}
	got = nil
	for _, it := range m.queueItems() {
		got = append(got, it.branch)
	}
	if len(got) != 3 {
		t.Fatalf("queue is %q, want feat back", got)
	}
}

func TestA2QueueRender(t *testing.T) {
	m, qv := a2QueueModel()
	m.focus = focusMain
	w := 100
	lines := qv.render(*m, w, 20)
	out := a2Plain(lines)
	for _, want := range []string{"Review queue", "3 things to look at", "1 waiting on you",
		"api · answering", "Claude Code waiting for an answer", "api · feat", "Codex finished", "3 files uncommitted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	for i, l := range lines[queueListTop:] {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("row %d is %d wide, want %d:\n%s", i, lw, w, out)
		}
	}
	// One machine's queue doesn't repeat its name on every row; a queue
	// spanning machines names them.
	if strings.Contains(out, "local · api") {
		t.Fatalf("single machine named on every row:\n%s", out)
	}
	box := m.machines[1]
	box.state = stateOnline
	box.projects = []proto.ProjectInfo{{ID: "r2", Name: "web", Git: true, Base: "main"}}
	if out := a2Plain(qv.render(*m, w, 20)); !strings.Contains(out, "local · api") || !strings.Contains(out, "box · web · ghost") {
		t.Fatalf("queue across machines unnamed:\n%s", out)
	}
	box.state = stateOffline

	// A short view scrolls to keep the selection visible. The selection
	// moves by key, as a person's would, so it carries its row with it.
	qv.key(m, a2Key("G"))
	lines = qv.render(*m, w, queueListTop+1)
	if len(lines) != queueListTop+1 || qv.scroll != 2 || !strings.Contains(a2Plain(lines), "uncommitted") {
		t.Fatalf("scroll %d:\n%s", qv.scroll, a2Plain(lines))
	}
	// Nothing waiting, and tiny sizes, both render.
	empty, _ := a2QueueModel()
	empty.machines[0].panes = nil
	empty.machines[0].projects[0].Branches = []proto.BranchInfo{{Name: "main"}}
	if out := a2Plain((&queueView{}).render(*empty, w, 10)); !strings.Contains(out, "Nothing is waiting for you") {
		t.Fatalf("empty queue:\n%s", out)
	}
	qv.render(*m, 10, 3)
	qv.render(*m, 1, 1)
}

// A narrow split keeps the branch — the one thing that says which row this
// is — and gives up the project and machine around it.
func TestA2QueueNarrowKeepsTheBranch(t *testing.T) {
	m, qv := a2QueueModel()
	m.machines[1].state = stateOnline // so rows carry a machine name too
	m.machines[1].projects = []proto.ProjectInfo{{ID: "r2", Name: "web", Git: true, Base: "main"}}

	wide := a2Plain(qv.render(*m, 110, 20))
	if !strings.Contains(wide, "local · api · answering") {
		t.Fatalf("wide render lost its context:\n%s", wide)
	}
	for _, w := range []int{70, 55, 44} {
		lines := qv.render(*m, w, 20)
		out := a2Plain(lines)
		if !strings.Contains(out, "answering") {
			t.Fatalf("at %d columns the branch is gone:\n%s", w, out)
		}
		for i, l := range lines[queueListTop:] {
			if lw := ansi.StringWidth(l); lw != w {
				t.Fatalf("at %d columns row %d is %d wide:\n%s", w, i, lw, out)
			}
		}
	}
	// Narrower still, something has to give, but nothing may overflow.
	for _, w := range []int{30, 20, 12} {
		for _, l := range qv.render(*m, w, 8) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("at %d columns a line is %d wide: %q", w, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
}

func TestA2QueueKeys(t *testing.T) {
	m, qv := a2QueueModel()
	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"down", 2}, {"up", 1}, {"k", 0}, {"pgdown", 2}, {"pgup", 0}, {"G", 2}, {"g", 0}, {"end", 2}, {"home", 0}} {
		if back, _ := qv.key(m, a2Key(step.key)); back || qv.sel != step.want {
			t.Fatalf("after %s sel is %d, want %d", step.key, qv.sel, step.want)
		}
	}
	for _, k := range []string{"esc", "q", "left", "h", "tab"} {
		if back, _ := qv.key(m, a2Key(k)); !back {
			t.Fatalf("%s should go back to the tree", k)
		}
	}
}

func TestA2QueueOpensWhereTheAnswerIs(t *testing.T) {
	m, qv := a2QueueModel()
	m.rebuild()

	// A waiting agent needs its pane: that is where the question is.
	qv.sel = 0
	if _, cmd := qv.key(m, a2Key("enter")); cmd != nil {
		a2Run(cmd)
	}
	if m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("waiting row opened %+v", m.tab().focused().view)
	}
	// Anything else needs the diff.
	qv.sel = 2
	if _, cmd := qv.key(m, a2Key("enter")); cmd != nil {
		a2Run(cmd)
	}
	v := m.tab().focused().view
	if v.Kind != kindBranch || v.Branch != "wip" {
		t.Fatalf("uncommitted row opened %+v", v)
	}
}

func TestA2QueueMouse(t *testing.T) {
	m, qv := a2QueueModel()
	m.rebuild()
	qv.render(*m, 100, 20) // the mouse handler works in rendered rows

	// A click selects; a second click on the same row opens it.
	qv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, queueListTop+1)
	if qv.sel != 1 {
		t.Fatalf("click selected %d", qv.sel)
	}
	if cmd := qv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, queueListTop+1); cmd == nil {
		t.Fatal("second click did nothing")
	}
	// Above the list, and past the end, change nothing.
	qv.sel = 1
	qv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, 0)
	qv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, queueListTop+40)
	if qv.sel != 1 {
		t.Fatalf("stray clicks moved the selection to %d", qv.sel)
	}
	// The wheel moves the selection and stops at the ends.
	qv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 5, 5)
	qv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 5, 5)
	if qv.sel != 2 {
		t.Fatalf("wheel down: %d", qv.sel)
	}
	for i := 0; i < 5; i++ {
		qv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, 5, 5)
	}
	if qv.sel != 0 {
		t.Fatalf("wheel up: %d", qv.sel)
	}
}

// The selection stays on the same row when the queue is rebuilt underneath
// it — rows are derived on every render, so an agent finishing elsewhere
// must not move what is selected.
func TestA2QueueSelectionFollowsItsRow(t *testing.T) {
	m, qv := a2QueueModel()
	qv.render(*m, 100, 20)
	qv.sel = 1
	qv.render(*m, 100, 20)
	was := qv.selKey

	now := time.Now()
	mach := m.machines[0]
	mach.panes = append([]proto.PaneInfo{{ID: "p9", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "newer",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Since: now.Add(-time.Hour)}}}, mach.panes...)
	qv.render(*m, 100, 20)
	if qv.selKey != was {
		t.Fatalf("selection jumped from %q to %q", was, qv.selKey)
	}
	if items := m.queueItems(); items[qv.sel].key() != was {
		t.Fatalf("selection is on %q, want %q", items[qv.sel].key(), was)
	}
}

// Q opens the queue in the focused split from anywhere in the tree, and
// keys then reach the view rather than the tree.
func TestA1QueueOpensWithQ(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Key(t, m, a2Key("Q"))
	v := m.tab().focused().view
	if v.Kind != kindReviewQueue || m.focus != focusMain || m.queueView == nil {
		t.Fatalf("Q opened %+v (focus %v, view %v)", v, m.focus, m.queueView != nil)
	}
	if title := m.leafTitle(m.tab().focused()); !strings.Contains(title, "review queue") {
		t.Fatalf("title %q", title)
	}
	// A movement key goes to the queue; esc hands focus back to the tree.
	before := m.queueView.sel
	a1Key(t, m, a2Key("j"))
	if m.focus != focusMain {
		t.Fatalf("j left the queue (sel %d → %d)", before, m.queueView.sel)
	}
	a1Key(t, m, a2Key("esc"))
	if m.focus != focusSidebar {
		t.Fatal("esc should go back to the tree")
	}
	// Opening it twice reuses the split rather than stacking tabs.
	tabs := len(m.tabs)
	a1Key(t, m, a2Key("Q"))
	a1Key(t, m, a2Key("Q"))
	if len(m.tabs) != tabs {
		t.Fatalf("tabs %d → %d", tabs, len(m.tabs))
	}
}

// x hides a row until what it says changes, which is what makes the queue
// a list to work through rather than a report to ignore.
func TestA2QueueDismiss(t *testing.T) {
	m, qv := a2QueueModel()
	qv.render(*m, 100, 20)
	before := len(m.queueItems())

	qv.key(m, a2Key("x")) // the waiting agent
	if n := len(m.queueItems()); n != before-1 {
		t.Fatalf("after x there are %d rows, want %d", n, before-1)
	}
	if !strings.Contains(m.flash, "dismissed answering") {
		t.Fatalf("flash %q", m.flash)
	}
	// It stays hidden while it says the same thing…
	qv.render(*m, 100, 20)
	if n := len(m.queueItems()); n != before-1 {
		t.Fatalf("it came back at once: %d rows", n)
	}
	// …and returns when it changes: the agent asks again later.
	for i := range m.machines[0].panes {
		if m.machines[0].panes[i].ID == "p1" {
			m.machines[0].panes[i].Agent.Since = time.Now()
		}
	}
	if n := len(m.queueItems()); n != before {
		t.Fatalf("a changed row stayed hidden: %d rows, want %d", n, before)
	}
	// Dismissing everything empties the queue, and the view says so.
	for i := 0; i < 5 && len(m.queueItems()) > 0; i++ {
		qv.key(m, a2Key("x"))
	}
	if out := a2Plain(qv.render(*m, 100, 10)); !strings.Contains(out, "Nothing is waiting for you") {
		t.Fatalf("emptied queue:\n%s", out)
	}
	// x on an empty queue does nothing rather than panicking.
	qv.key(m, a2Key("x"))
}

// The status bar counts what the queue would show, and clicking it opens
// the queue.
func TestA1QueueCountInStatusBar(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// The fixture's agents are idle and its branches shipped, so give it
	// one thing to review: an agent that finished while nobody looked.
	for i := range m.machines[0].panes {
		if m.machines[0].panes[i].ID == "p1" {
			m.machines[0].panes[i].Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: time.Now().Add(-time.Hour)}
		}
	}
	items := m.queueItems()
	if len(items) != 1 {
		t.Fatalf("expected one thing to review, got %+v", items)
	}
	var chip *statusItem
	right := m.statusRightItems(rightFull)
	for i := range right {
		if strings.Contains(ansi.Strip(right[i].text), "to review") {
			chip = &right[i]
		}
	}
	if chip == nil {
		t.Fatalf("no review chip among %d items", len(m.statusRightItems(rightFull)))
	}
	if want := fmt.Sprintf("%d to review", len(items)); !strings.Contains(ansi.Strip(chip.text), want) {
		t.Fatalf("chip %q, want %q", ansi.Strip(chip.text), want)
	}
	if cmd := chip.act(m); cmd != nil {
		a2Run(cmd)
	}
	if v := m.tab().focused().view; v.Kind != kindReviewQueue || m.focus != focusMain {
		t.Fatalf("the chip opened %+v", v)
	}
	// A narrow status bar drops it rather than overflowing.
	for _, it := range m.statusRightItems(rightNoExtras) {
		if strings.Contains(ansi.Strip(it.text), "to review") {
			t.Fatal("the count survived into a narrow status bar")
		}
	}
	// With nothing to review there is no chip at all.
	for _, mach := range m.machines {
		mach.panes, mach.projects = nil, nil
	}
	for _, it := range m.statusRightItems(rightFull) {
		if strings.Contains(ansi.Strip(it.text), "to review") {
			t.Fatal("a chip with an empty queue")
		}
	}
}
