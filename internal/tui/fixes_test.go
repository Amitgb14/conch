package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// x on a branch whose worktree was already removed used to say only linked
// worktrees can be removed, leaving no way to be rid of the branch.
func TestA2RemoveBranchWithoutWorktree(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client("session.v1", harvestCapability)
	m.showAll[scoped(localMachine, "r1")] = true // feat is behind "more" otherwise
	m.rebuild()
	// feat has a worktree: x removes that, and says the branch stays.
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("removing a worktree should only confirm")
	}
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " ")+d.title, "worktree") {
		t.Fatalf("worktree confirm: %#v", m.overlay)
	}
	m.overlay = nil

	// The same branch with its worktree gone: x offers to delete the branch.
	m.machines[0].projects[0].Worktrees = []proto.WorktreeInfo{{Path: "/src/api", Branch: "main", Main: true}}
	m.rebuild()
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	cmd := m.openRemove()
	if cmd == nil {
		t.Fatal("a branch with no worktree should still be removable")
	}
	if m.flash != "" {
		t.Fatalf("unexpected flash: %q", m.flash)
	}

	// The base branch is never offered.
	m.flash = ""
	m.cursor = branchNodeID(localMachine, "r1", "main")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("the base branch should not be discarded")
	}
	if !strings.Contains(m.flash, "base branch") {
		t.Fatalf("flash: %q", m.flash)
	}

	// A server too old to discard says so instead of doing nothing.
	m.machines[0].c = a2Client("session.v1")
	m.flash = ""
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("no discard without the capability")
	}
	if !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash: %q", m.flash)
	}
}

// The branch menu offers the same thing the key does.
func TestA2BranchMenuFollowsTheWorktree(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client(harvestCapability)
	r := row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}
	labels := func() string {
		var out []string
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			out = append(out, it.label)
		}
		return strings.Join(out, " | ")
	}
	if got := labels(); !strings.Contains(got, "Remove worktree") {
		t.Fatalf("with a worktree: %s", got)
	}
	m.machines[0].projects[0].Worktrees = []proto.WorktreeInfo{{Path: "/src/api", Branch: "main", Main: true}}
	if got := labels(); !strings.Contains(got, "Delete branch") || strings.Contains(got, "Remove worktree") {
		t.Fatalf("without a worktree: %s", got)
	}
	// Nothing to delete on the base branch.
	r.branch = "main"
	if got := labels(); strings.Contains(got, "Delete branch") {
		t.Fatalf("base branch: %s", got)
	}
}

// A new tab starts a shell; when the shell never starts (the machine went
// away) the tab goes with it, instead of sitting there empty for good.
func TestA2FailedTabCloses(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2") // a running terminal
	for _, r := range m.rows {
		if r.id == m.cursor {
			a2Run(m.show(r))
		}
	}
	before := len(m.tabs)
	m.newTab(viewRef{})
	if len(m.tabs) != before+1 {
		t.Fatalf("no tab opened: %d", len(m.tabs))
	}
	if !m.tab().focused().await {
		t.Fatal("a tab waiting for its shell should say so")
	}
	next, _ := m.Update(errMsg{errString("machine is offline")})
	*m = next.(Model)
	if len(m.tabs) != before {
		t.Fatalf("the empty tab stayed: %d tabs", len(m.tabs))
	}
	if !strings.Contains(m.flash, "offline") {
		t.Fatalf("the error should still be shown: %q", m.flash)
	}
}

// The pane arriving clears the mark, so a later error leaves the tab alone.
func TestA2ArrivedKeepsTheTab(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2")
	m.newTab(viewRef{})
	m.tab().focused().await = true
	info := proto.PaneInfo{ID: "p9", Name: "zsh", State: proto.PaneRunning}
	next, _ := m.Update(createdMsg{machine: localMachine, info: info})
	*m = next.(Model)
	before := len(m.tabs)
	next, _ = m.Update(errMsg{errString("something else went wrong")})
	*m = next.(Model)
	if len(m.tabs) != before {
		t.Fatalf("a tab with a pane should stay: %d, want %d", len(m.tabs), before)
	}
}

// The last tab is never closed, however it failed: conch always has one.
func TestA2FailedTabKeepsTheLastOne(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.tabs = m.tabs[:0]
	m.newTab(viewRef{})
	m.tab().focused().await = true
	if len(m.tabs) != 1 {
		t.Fatalf("want one tab, got %d", len(m.tabs))
	}
	next, _ := m.Update(errMsg{errString("offline")})
	*m = next.(Model)
	if len(m.tabs) != 1 {
		t.Fatalf("the last tab was closed: %d", len(m.tabs))
	}
	if m.tabs[0].focused().await {
		t.Fatal("the mark should be cleared once it is given up on")
	}
}

// A split half that never gets its pane closes too, leaving the other half.
func TestA2FailedSplitCloses(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2")
	m.newTab(viewRef{Row: paneNodeID(localMachine, "p2"), Kind: kindPane, Machine: localMachine, PaneID: "p2"})
	t.Cleanup(func() { m.tabs = nil })
	m.split(splitRight, viewRef{})
	if n := len(m.tab().root.leaves()); n != 2 {
		t.Fatalf("want two halves, got %d", n)
	}
	next, _ := m.Update(errMsg{errString("offline")})
	*m = next.(Model)
	if n := len(m.tab().root.leaves()); n != 1 {
		t.Fatalf("the empty half stayed: %d halves", n)
	}
}

// ctrl+b c opens a terminal wherever the tree is pointing. It used to leave
// an empty tab when there was no pane beside it — and the tab bar, which
// lists only the selected group's tabs, hid them until a pane brought that
// group on screen, so half a dozen appeared at once.
func TestA2NewTabAlwaysStartsAShell(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = branchNodeID(localMachine, "r1", "main") // a tree row, not a pane
	m.tabs = m.tabs[:0]
	if cmd := m.newShellTab(); cmd == nil {
		t.Fatal("want a shell started with the new tab")
	}
	if len(m.tabs) != 1 {
		t.Fatalf("%d tabs, want 1", len(m.tabs))
	}
	l := m.tab().focused()
	if !l.view.empty() {
		t.Fatalf("the tab should be waiting for its pane: %+v", l.view)
	}
	if !l.await {
		t.Fatal("a tab waiting for a shell has to be marked, or it lingers empty")
	}

	// Three in a row: three shells on the way, no tab left behind.
	m.newShellTab()
	m.newShellTab()
	if len(m.tabs) != 3 {
		t.Fatalf("%d tabs, want 3", len(m.tabs))
	}
	for i, tab := range m.tabs {
		if !tab.focused().await {
			t.Fatalf("tab %d is not waiting for anything", i+1)
		}
	}
	// And each failure takes one away, rather than leaving them to pile up.
	for want := 2; want >= 1; want-- {
		next, _ := m.Update(errMsg{errString("offline")})
		*m = next.(Model)
		if len(m.tabs) != want {
			t.Fatalf("after a failure: %d tabs, want %d", len(m.tabs), want)
		}
	}
}

// The + menu's "Empty tab" still means an empty tab: it is the one place
// that asks for one.
func TestA2EmptyTabStaysAvailable(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.tabs = m.tabs[:0]
	if cmd := m.newTab(viewRef{}); cmd == nil {
		t.Fatal("newTab should still focus the tab it made")
	}
	if len(m.tabs) != 1 || !m.tab().focused().view.empty() {
		t.Fatalf("want one empty tab, got %d", len(m.tabs))
	}
	if m.tab().focused().await {
		t.Fatal("an empty tab asked for on purpose is not waiting for a pane")
	}
}
