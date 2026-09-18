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

func staleList() []proto.StaleWorktree {
	return []proto.StaleWorktree{
		{Path: "/src/api.worktrees/done", Branch: "done", Merged: true, Reasons: []string{"merged into main"}, Suggested: true,
			Committed: time.Now().Add(-49 * time.Hour)},
		{Path: "/src/api.worktrees/wip", Branch: "wip", Uncommitted: 2, Reasons: []string{"no commits of its own"}},
		{Path: "/src/api.worktrees/ahead", Branch: "ahead", Unmerged: 1},
		{Path: "/src/api-feat", Branch: "feat", Panes: true},
		{Path: "/src/api.worktrees/old", Reasons: []string{"folder gone", "detached"}, Missing: true, Suggested: true},
		{Path: "/src/api.worktrees/main", Branch: "main", Base: true},
	}
}

func openedCleanup(t *testing.T, m *Model) *cleanupDialog {
	t.Helper()
	next, _ := m.update(cleanupListMsg{machine: localMachine, projectID: "r1", worktrees: staleList()})
	*m = next.(Model)
	d, ok := m.overlay.(*cleanupDialog)
	if !ok {
		t.Fatalf("no cleanup list: %T %q", m.overlay, m.flash)
	}
	return d
}

func TestCleanupOpens(t *testing.T) {
	m, peer := harvestModel(t, cleanupCapability)
	a1At(t, m, projectNodeID(localMachine, "r1"))
	cmd := a1Key(t, m, a2Key("W"))
	if m.flash != "reading the worktrees of api…" || cmd == nil {
		t.Fatalf("W: %q", m.flash)
	}
	peer.setResult(proto.MethodWorktreeStale, proto.WorktreeStale{Worktrees: staleList()})
	msgs := a2Run(cmd)
	if lm, ok := msgs[0].(cleanupListMsg); !ok || len(lm.worktrees) != 6 || lm.projectID != "r1" {
		t.Fatalf("list: %v", msgs)
	}
	if !strings.Contains(string(peer.waitMethod(t, proto.MethodWorktreeStale, "").Params), `"r1"`) {
		t.Fatal("asked for another project")
	}
	next, _ := m.update(cleanupListMsg{machine: localMachine, projectID: "r1"})
	*m = next.(Model)
	if m.overlay != nil || m.flash != "api has no worktrees besides its main checkout" {
		t.Fatalf("empty: %T %q", m.overlay, m.flash)
	}
	peer.setError(proto.MethodWorktreeStale, "git broke")
	if !strings.Contains(a2ErrText(a2Run(m.openCleanup())), "git broke") {
		t.Fatal("error not shown")
	}

	// The project menu offers it.
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindProject, machine: localMachine, projectID: "r1"}, 0, 0)); !strings.Contains(got, "W Clean up worktrees…") {
		t.Fatalf("menu:\n%s", got)
	}

	old, _ := harvestModel(t, harvestCapability)
	a1At(t, old, projectNodeID(localMachine, "r1"))
	if old.openCleanup() != nil || !strings.Contains(old.flash, "predates cleaning up") {
		t.Fatalf("old server: %q", old.flash)
	}
	offline, _ := a1Fixture(t, false)
	a1At(t, offline, projectNodeID(localMachine, "r1"))
	if offline.openCleanup() != nil || !strings.Contains(offline.flash, "local is") {
		t.Fatalf("offline: %q", offline.flash)
	}
	a1At(t, m, paneNodeID(localMachine, "p3")) // outside every project
	if m.openCleanup() != nil || !strings.Contains(m.flash, "select a git project") {
		t.Fatalf("no project: %q", m.flash)
	}
}

func TestCleanupTicks(t *testing.T) {
	m, _ := harvestModel(t, cleanupCapability)
	d := openedCleanup(t, m)
	if names := cleanupNames(d.chosen()); names != "done,old" {
		t.Fatalf("suggested ticks: %s", names)
	}
	// feat has panes: it can't be ticked.
	d.sel = 3
	d.update(m, a2Key(" "))
	if d.on[3] || !strings.Contains(d.err, "feat can't be removed: panes running") {
		t.Fatalf("blocked: %v %q", d.on, d.err)
	}
	for _, k := range []string{"up", "k", "j", "x"} { // back up to ahead, tick it
		d.update(m, a2Key(k))
	}
	if d.sel != 2 || !d.on[2] || d.err != "" {
		t.Fatalf("ahead: sel %d on %v err %q", d.sel, d.on, d.err)
	}
	// With the finished ones already ticked, a unticks everything; again it
	// ticks just the finished ones.
	d.update(m, a2Key("a"))
	if len(d.chosen()) != 0 {
		t.Fatalf("a: %s", cleanupNames(d.chosen()))
	}
	d.update(m, a2Key("a"))
	if names := cleanupNames(d.chosen()); names != "done,old" {
		t.Fatalf("a again: %s", names)
	}
	d.update(m, a2Key("a"))
	d.update(m, a2Key("enter"))
	if d.err != "tick at least one (space)" || m.overlay != d {
		t.Fatalf("nothing ticked: %q %T", d.err, m.overlay)
	}
	for _, k := range []string{"down", "down", "down", "down", "down", "down", "j"} {
		d.update(m, a2Key(k))
	}
	if d.sel != 5 {
		t.Fatalf("sel past the end: %d", d.sel)
	}
	if closed, _ := d.update(m, a2Key("esc")); !closed || m.overlay != nil {
		t.Fatal("esc closes")
	}
}

func cleanupNames(items []proto.StaleWorktree) string {
	var names []string
	for _, w := range items {
		names = append(names, strings.TrimSuffix(worktreeName(w), " (detached)"))
	}
	return strings.Join(names, ",")
}

func TestCleanupReviewAndRun(t *testing.T) {
	m, peer := harvestModel(t, cleanupCapability)
	d := openedCleanup(t, m)

	// Only safe ones: enter confirms, nothing is forced.
	d.update(m, a2Key("enter"))
	c, ok := m.overlay.(*cleanupConfirm)
	if !ok || c.yesOnly {
		t.Fatalf("safe confirm: %T", m.overlay)
	}
	if q := strings.Join(c.text, " "); q != "Remove 2 worktrees and delete their branches: done, old (detached)?" {
		t.Fatalf("question %q", q)
	}
	// n goes back to the list with the ticks kept.
	c.update(m, a2Key("n"))
	if m.overlay != d || len(d.chosen()) != 2 {
		t.Fatalf("n: %T", m.overlay)
	}

	// Ticking work that would be lost asks for y and forces only those.
	d.on[1], d.on[2], d.on[5] = true, true, true
	d.update(m, a2Key("enter"))
	c = m.overlay.(*cleanupConfirm)
	text := strings.Join(c.text, " ")
	for _, want := range []string{"Remove 5 worktrees and delete their branches: done, wip, ahead, old (detached), main?",
		"This loses wip (2 uncommitted files), ahead (1 commit not merged or pushed), for good."} {
		if !strings.Contains(text, want) {
			t.Fatalf("confirm %q lacks %q", text, want)
		}
	}
	if !c.yesOnly {
		t.Fatal("a lossy cleanup takes enter")
	}
	if _, cmd := c.update(m, a2Key("enter")); cmd != nil || m.overlay != c {
		t.Fatal("enter ran a lossy cleanup")
	}
	peer.setResult(proto.MethodWorktreeCleanup, proto.WorktreeCleanupResult{
		Removed: []string{"/src/api.worktrees/done", "/src/api.worktrees/old"},
		Failed:  []proto.CleanupFailure{{Path: "/src/api.worktrees/wip", Error: "would lose 3 uncommitted files"}}})
	_, cmd := c.update(m, a2Key("y"))
	if m.flash != "removing 5 worktrees…" {
		t.Fatalf("flash %q", m.flash)
	}
	msgs := a2Run(cmd)
	p := lastParams[proto.WorktreeCleanupParams](t, peer, proto.MethodWorktreeCleanup)
	var forced []string
	for _, r := range p.Remove {
		if r.Force {
			forced = append(forced, r.Path)
		}
	}
	if p.ProjectID != "r1" || len(p.Remove) != 5 || strings.Join(forced, ",") != "/src/api.worktrees/wip,/src/api.worktrees/ahead" {
		t.Fatalf("params %+v", p)
	}
	next, _ := m.update(msgs[0])
	*m = next.(Model)
	if m.flash != "removed 2 worktrees; kept wip: would lose 3 uncommitted files" || !m.flashIsErr {
		t.Fatalf("result flash %q", m.flash)
	}
	m.receiveCleanup(cleanupDoneMsg{result: proto.WorktreeCleanupResult{Removed: []string{"/x"}}})
	if m.flash != "removed 1 worktree" || m.flashIsErr {
		t.Fatalf("clean result %q", m.flash)
	}

	// One worktree, a server error.
	d = openedCleanup(t, m)
	d.on = make([]bool, len(d.items))
	d.on[0] = true
	d.update(m, a2Key("enter"))
	if q := strings.Join(m.overlay.(*cleanupConfirm).text, " "); !strings.HasPrefix(q, "Remove 1 worktree and delete its branch: done?") {
		t.Fatalf("one: %q", q)
	}
	peer.setError(proto.MethodWorktreeCleanup, "no project")
	_, cmd = m.overlay.(*cleanupConfirm).update(m, a2Key("y"))
	if !strings.Contains(a2ErrText(a2Run(cmd)), "no project") {
		t.Fatal("server error not shown")
	}
}

func TestCleanupRenderAndMouse(t *testing.T) {
	m, _ := harvestModel(t, cleanupCapability)
	d := openedCleanup(t, m)
	out := a2Plain(d.render(*m).lines)
	for _, want := range []string{"2 of 6 worktrees of api ticked", "[x] done", "merged into main", "[ ] wip", "2 uncommitted files",
		"1 commit not merged or pushed", "panes running", "old (detached)", "folder gone · detached", "/src/api.worktrees/done · last commit 2d"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	d.sel = 5
	if out := a2Plain(d.render(*m).lines); !strings.Contains(out, "the base branch is kept") {
		t.Fatalf("base note:\n%s", out)
	}

	// Long lists scroll, and every size keeps lines inside the box.
	var many []proto.StaleWorktree
	for i := 0; i < 30; i++ {
		many = append(many, proto.StaleWorktree{Path: fmt.Sprintf("/w/b%02d", i), Branch: fmt.Sprintf("branch-with-a-long-name-%02d", i),
			Reasons: []string{"merged into main", "pull request merged", "upstream deleted"}, Suggested: i%2 == 0})
	}
	for _, size := range [][2]int{{20, 5}, {40, 20}, {160, 40}} {
		m.width, m.height = size[0], size[1]
		big := newCleanupDialog(cleanupListMsg{machine: localMachine, projectID: "r1", worktrees: many})
		for i := 0; i < 29; i++ {
			big.update(m, a2Key("down"))
		}
		b := big.render(*m)
		w := b.width()
		for _, l := range b.lines {
			if ansi.StringWidth(l) != w {
				t.Fatalf("%v: line %q is %d wide, box %d", size, ansi.Strip(l), ansi.StringWidth(l), w)
			}
		}
		if out := a2Plain(b.lines); !strings.Contains(out, "… 18 above") || !strings.Contains(out, "branch-with-a-long-name-29"[:10]) {
			t.Fatalf("%v scrolled:\n%s", size, out)
		}
	}

	// A click on a row selects and toggles it; a click outside closes.
	m.width, m.height = 160, 40
	d = openedCleanup(t, m)
	b := d.render(*m)
	d.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 3 + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if d.sel != 2 || !d.on[2] {
		t.Fatalf("click: sel %d on %v", d.sel, d.on)
	}
	d.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if d.sel != 2 {
		t.Fatal("a click above the list changed the selection")
	}
	d.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 3 + 2, Action: tea.MouseActionMotion}, b)
	if !d.on[2] {
		t.Fatal("motion toggled")
	}
	d.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("outside click keeps the list")
	}
}

// The confirmation is answered by mouse too: Amit clicks rather than types,
// and a click here removes worktrees for good.
func TestCleanupConfirmMouse(t *testing.T) {
	m, peer := harvestModel(t, cleanupCapability)
	m.width, m.height = 160, 40
	press := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	}
	confirm := func(d *cleanupDialog) (*cleanupConfirm, box) {
		t.Helper()
		d.update(m, a2Key("enter"))
		c, ok := m.overlay.(*cleanupConfirm)
		if !ok {
			t.Fatalf("no confirmation: %T", m.overlay)
		}
		return c, c.render(*m)
	}

	// No goes back to the list with its ticks, as n does — not to no overlay
	// at all, which is what the plain dialog would do.
	d := openedCleanup(t, m)
	c, b := confirm(d)
	if cmd := c.mouse(m, press(b.x+1+c.buttons.no0, b.y+1+c.buttons.line), b); cmd != nil {
		t.Fatal("No ran the cleanup")
	}
	if m.overlay != d || len(d.chosen()) != 2 {
		t.Fatalf("No: %T with %d ticked", m.overlay, len(d.chosen()))
	}

	// A click outside the box also returns to the list, keeping the ticks.
	c, b = confirm(d)
	c.mouse(m, press(0, 0), b)
	if m.overlay != d || len(d.chosen()) != 2 {
		t.Fatalf("outside: %T with %d ticked", m.overlay, len(d.chosen()))
	}

	// Anything but a left press is ignored, so a drag or a right-click over
	// Yes can't remove a worktree.
	c, b = confirm(d)
	yes := press(b.x+1+c.buttons.yes0, b.y+1+c.buttons.line)
	for _, msg := range []tea.MouseMsg{
		{X: yes.X, Y: yes.Y, Action: tea.MouseActionMotion},
		{X: yes.X, Y: yes.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight},
		{X: yes.X, Y: yes.Y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
	} {
		if cmd := c.mouse(m, msg, b); cmd != nil || m.overlay != c {
			t.Fatalf("%v acted", msg.Action)
		}
	}
	// A press on neither button, and one on the buttons' line but outside
	// them, leave the confirmation open.
	for _, p := range []tea.MouseMsg{press(b.x+1+c.buttons.yes0, b.y+1+c.buttons.line-1), press(b.x+1+c.buttons.no1+4, b.y+1+c.buttons.line)} {
		if cmd := c.mouse(m, p, b); cmd != nil || m.overlay != c {
			t.Fatalf("stray click at %d,%d acted: %T", p.X, p.Y, m.overlay)
		}
	}

	// Yes removes them, as y does.
	peer.setResult(proto.MethodWorktreeCleanup, proto.WorktreeCleanupResult{Removed: []string{"/src/api.worktrees/done", "/src/api.worktrees/old"}})
	cmd := c.mouse(m, yes, b)
	if cmd == nil || m.overlay != nil || m.flash != "removing 2 worktrees…" {
		t.Fatalf("Yes: cmd %v overlay %T flash %q", cmd != nil, m.overlay, m.flash)
	}
	msgs := a2Run(cmd)
	p := lastParams[proto.WorktreeCleanupParams](t, peer, proto.MethodWorktreeCleanup)
	if len(p.Remove) != 2 {
		t.Fatalf("removed %+v", p.Remove)
	}
	next, _ := m.update(msgs[0])
	*m = next.(Model)
	if m.flash != "removed 2 worktrees" || m.flashIsErr {
		t.Fatalf("result flash %q", m.flash)
	}
}
