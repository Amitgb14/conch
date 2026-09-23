package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

// a5Diff is a one-hunk diff of file whose added lines are the ones given.
func a5Diff(file string, added ...string) string {
	d := fmt.Sprintf("diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n@@ -1,1 +1,%d @@\n context\n", file, file, file, file, len(added)+1)
	for _, l := range added {
		d += "+" + l + "\n"
	}
	return d
}

// a5Live is a changes view with a.go's diff open on a checked-out branch.
func a5Live(m *Model, added ...string) *changesView {
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	d := a2Changes("a.go")
	d.Watched = true
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: d})
	cv.key(m, a2Key("enter"))
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a5Diff("a.go", added...)})
	return cv
}

func TestA5FreshLines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prev, next []string
		want       []int
	}{
		{"the first read marks nothing", nil, []string{"+one", "-two"}, nil},
		{"an unchanged diff marks nothing", []string{"+one"}, []string{"+one"}, nil},
		{"a new added line", []string{"+one"}, []string{"+one", "+two"}, []int{1}},
		{"a new removed line", []string{" ctx"}, []string{" ctx", "-gone"}, []int{1}},
		{"a rewritten line", []string{"+old"}, []string{"+new"}, []int{0}},
		{"lines that only moved", []string{"+a", "+b"}, []string{"+b", "+a"}, nil},
		{"the same line written twice", []string{"+a"}, []string{"+a", "+a"}, []int{1}},
		{"context and hunk headers never count", []string{}, []string{"@@ -1 +9 @@", " ctx", "diff --git a b"}, nil},
		{"file headers never count", []string{}, []string{"--- a/x", "+++ b/x"}, nil},
		{"an empty line is not content", []string{}, []string{""}, nil},
		{"everything gone", []string{"+one"}, []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := freshLines(tc.prev, tc.next)
			if len(fresh) != len(tc.want) {
				t.Fatalf("fresh %v, want %v", fresh, tc.want)
			}
			for _, i := range tc.want {
				if !fresh[i] {
					t.Fatalf("line %d not fresh: %v", i, fresh)
				}
			}
		})
	}
}

func TestA5DiffLines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"\n\n", []string{}},
		{"one", []string{"one"}},
		{"one\n", []string{"one"}},
		{"one\ntwo\n\n", []string{"one", "two"}},
		{"\none", []string{"", "one"}},
	} {
		got := diffLines(tc.in)
		if got == nil || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("diffLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestA5LiveDiffFetch(t *testing.T) {
	m := a2Model()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	if cv.liveDiff(m) != nil {
		t.Fatal("no open diff to re-read")
	}
	d := a2Changes("a.go")
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: d})
	cv.key(m, a2Key("enter")) // opens a.go, leaving the first read in flight
	if cv.liveDiff(m) != nil {
		t.Fatal("re-read while the reader's own read is in flight")
	}
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a5Diff("a.go", "one")})
	if cv.loadingDiff {
		t.Fatal("the first read is still marked in flight")
	}

	cmd := cv.liveDiff(m)
	if cmd == nil || !cv.diffLive {
		t.Fatal("an open diff re-reads itself")
	}
	if cv.liveDiff(m) != nil {
		t.Fatal("one background re-read at a time")
	}
	msg := a2Run(cmd)[0].(diffMsg)
	if !msg.live || msg.file != "a.go" || msg.err == nil {
		t.Fatalf("live read offline: %+v", msg)
	}
	// A failed background read keeps the diff on screen and says nothing.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, err: errors.New("flaky")})
	if cv.diffErr != "" || len(cv.diff) == 0 || cv.diffLive {
		t.Fatalf("live error: %q %d %v", cv.diffErr, len(cv.diff), cv.diffLive)
	}
	// The reader's own re-read does show its error.
	if cv.refreshDiff(m) == nil || !cv.loadingDiff {
		t.Fatal("R re-reads the open diff")
	}
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", err: errors.New("too big")})
	if cv.diffErr != "too big" || cv.loadingDiff {
		t.Fatalf("read error: %q", cv.diffErr)
	}
	// A later good read clears it.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one")})
	if cv.diffErr != "" {
		t.Fatalf("stale error %q", cv.diffErr)
	}
	// A reply for a file that is no longer open leaves the flag set for the
	// read that replaced it.
	cv.liveDiff(m)
	cv.diffFile = "b.go"
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: "x"})
	if !cv.diffLive {
		t.Fatal("a stale reply cleared the read in flight")
	}
}

func TestA5FreshMarks(t *testing.T) {
	m := a2Model()
	cv := a5Live(m, "one")
	if len(cv.fresh) != 0 || !cv.freshUntil.IsZero() {
		t.Fatalf("the first read marked %v", cv.fresh)
	}
	out := a2Plain(cv.renderDiff(*m, 80, 20))
	if strings.Contains(out, "just changed") || strings.Contains(out, "▌") {
		t.Fatalf("first read:\n%s", out)
	}

	// The agent writes another line: it is marked, and counted in the header.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one", "two")})
	if len(cv.fresh) != 1 {
		t.Fatalf("fresh %v", cv.fresh)
	}
	out = a2Plain(cv.renderDiff(*m, 80, 20))
	if !strings.Contains(out, "1 line just changed") || !strings.Contains(out, "▌+two") {
		t.Fatalf("one fresh line:\n%s", out)
	}
	if strings.Contains(out, "▌+one") {
		t.Fatal("a line that did not change was marked")
	}

	// Two more, and the plural.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one", "two", "three", "four")})
	if out = a2Plain(cv.renderDiff(*m, 80, 20)); !strings.Contains(out, "2 lines just changed") {
		t.Fatalf("two fresh lines:\n%s", out)
	}

	// The marks fade on their own.
	cv.freshUntil = time.Now().Add(-time.Millisecond)
	if out = a2Plain(cv.renderDiff(*m, 80, 20)); strings.Contains(out, "just changed") || strings.Contains(out, "▌") {
		t.Fatalf("faded:\n%s", out)
	}
	// A read that changes nothing leaves the old marks alone rather than
	// re-arming them.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one", "two", "three", "four")})
	if out = a2Plain(cv.renderDiff(*m, 80, 20)); strings.Contains(out, "just changed") {
		t.Fatalf("an unchanged read re-armed the marks:\n%s", out)
	}
}

func TestA5Follow(t *testing.T) {
	m := a2Model()
	var many []string
	for i := 0; i < 60; i++ {
		many = append(many, fmt.Sprintf("line %d", i))
	}
	cv := a5Live(m, many...)
	if cv.noFollow || cv.diffScroll != 0 {
		t.Fatal("a freshly opened diff follows from the top")
	}
	if out := a2Plain(cv.renderDiff(*m, 80, 20)); !strings.Contains(out, "following") {
		t.Fatalf("hint:\n%s", out)
	}

	// A line written far below scrolls into view.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", append(many, "written last")...)})
	cv.renderDiff(*m, 80, 20)
	if cv.diffScroll == 0 {
		t.Fatal("the view did not follow the new line")
	}
	first := 0
	for i, l := range cv.diff {
		if strings.Contains(l, "written last") {
			first = i
		}
	}
	if first < cv.diffScroll || first >= cv.diffScroll+19 {
		t.Fatalf("line %d not on screen from %d", first, cv.diffScroll)
	}
	if cv.followTo != 0 {
		t.Fatal("the follow was not spent")
	}

	// Scrolling stops it; a new line no longer moves the view.
	cv.key(m, a2Key("up"))
	if !cv.noFollow {
		t.Fatal("scrolling still follows")
	}
	if out := a2Plain(cv.renderDiff(*m, 80, 20)); !strings.Contains(out, "F follow") {
		t.Fatalf("stopped hint:\n%s", out)
	}
	at := cv.diffScroll
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", append(many, "written last", "and another")...)})
	cv.renderDiff(*m, 80, 20)
	if cv.diffScroll != at {
		t.Fatalf("the view moved to %d while not following", cv.diffScroll)
	}

	// g and F start following again; F turns it off.
	cv.key(m, a2Key("g"))
	if cv.noFollow || cv.diffScroll != 0 {
		t.Fatal("g goes back to the top and follows")
	}
	cv.key(m, a2Key("F"))
	if !cv.noFollow {
		t.Fatal("F stops following")
	}
	cv.key(m, a2Key("F"))
	if cv.noFollow {
		t.Fatal("F follows again")
	}
	// Every other way of moving stops it too.
	for _, k := range []string{"down", "j", "k", "pgup", "pgdown", "b", "f", "G", "end", "n", "N", "[", "]"} {
		cv.noFollow = false
		if cv.key(m, a2Key(k)); !cv.noFollow {
			t.Fatalf("%s still follows", k)
		}
	}
	// So does the wheel.
	cv.noFollow = false
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, 0, 5)
	if !cv.noFollow {
		t.Fatal("the wheel still follows")
	}
	// Opening another file starts over.
	cv.noFollow = true
	cv.data.Files = append(cv.data.Files, proto.FileChange{Path: "b.go", Code: "M"})
	cv.key(m, a2Key("esc"))
	cv.sel = 1
	cv.key(m, a2Key("enter"))
	if cv.diffFile != "b.go" {
		t.Fatalf("opened %q", cv.diffFile)
	}
	if cv.noFollow || cv.fresh != nil || !cv.freshUntil.IsZero() || cv.followTo != 0 {
		t.Fatalf("another file kept %v %v", cv.noFollow, cv.fresh)
	}
}

func TestA5DiffGoesEmpty(t *testing.T) {
	m := a2Model()
	cv := a5Live(m, "one")
	// The agent undid its edit, or it was committed underneath.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: ""})
	if len(cv.diff) != 0 || cv.diff == nil {
		t.Fatalf("empty diff %v", cv.diff)
	}
	out := a2Plain(cv.renderDiff(*m, 80, 20))
	if !strings.Contains(out, "no changes in this file") || strings.Contains(out, "reading the diff") {
		t.Fatalf("empty:\n%s", out)
	}
	// It is still an open diff, so it keeps re-reading itself.
	if cv.liveDiff(m) == nil {
		t.Fatal("an emptied diff stopped re-reading")
	}
}

func TestA5LiveDiffTinyAndNarrow(t *testing.T) {
	m := a2Model()
	cv := a5Live(m, "one", "two", "three")
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one", "two", "three", "four")})
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 3}, {200, 60}} {
		w, h := size[0], size[1]
		lines := cv.renderDiff(*m, w, h)
		if len(lines) > h {
			t.Fatalf("%dx%d rendered %d lines", w, h, len(lines))
		}
		// The body is clipped to the split by fit(); the header this view
		// lays out itself has to stay inside w on its own.
		if got := ansi.StringWidth(lines[0]); got > w {
			t.Fatalf("%dx%d header %q is %d wide", w, h, ansi.Strip(lines[0]), got)
		}
	}
	// A follow target left over from a diff that has since shrunk cannot
	// scroll past the end.
	cv.followTo = 500
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", live: true, diff: a5Diff("a.go", "one")})
	if cv.followTo != 0 {
		t.Fatalf("a stale follow survived: %d", cv.followTo)
	}
	cv.followTo = 500
	cv.renderDiff(*m, 80, 20)
	if cv.diffScroll < 0 || cv.diffScroll > len(cv.diff) {
		t.Fatalf("followed to %d of %d lines", cv.diffScroll, len(cv.diff))
	}
}

func TestA5PollRereadsOpenDiff(t *testing.T) {
	m := a2Model()
	cv := a5Live(m, "one")
	m.tabs = []*tab{{name: "1", root: &layoutNode{leaf: &leaf{id: 1, changes: cv}}, focus: 1}}
	_, cmd := m.Update(changesPollMsg{})
	live := false
	for _, msg := range a2Run(cmd) {
		if dm, ok := msg.(diffMsg); ok && dm.file == "a.go" && dm.live {
			live = true
		}
	}
	if !live {
		t.Fatal("the poll tick did not re-read the open diff")
	}
	// A view with no diff open only re-reads the branch.
	cv.diffFile, cv.diffLive = "", false
	_, cmd = m.Update(changesPollMsg{})
	for _, msg := range a2Run(cmd) {
		if _, ok := msg.(diffMsg); ok {
			t.Fatal("re-read a diff that is not open")
		}
	}
}

// a5WatchModel is a model showing a.go's diff on feat's worktree, with the
// machine's client claiming caps.
func a5WatchModel(t *testing.T, caps ...string) (*Model, *changesView) {
	t.Helper()
	m := a2Model()
	m.machines[0].c = a2Client(caps...)
	cv := a5Live(m, "one")
	m.tabs = []*tab{{name: "1", root: &layoutNode{leaf: &leaf{id: 1, changes: cv}}, focus: 1}}
	return m, cv
}

func TestA5WorktreeChangedEvent(t *testing.T) {
	m, cv := a5WatchModel(t, proto.CapWorktreeWatch)
	mach := m.machines[0]
	wt := cv.data.Worktree // "/src/api-feat"

	// The commands are not run: this model's client was never connected.
	// What they are is read off the view instead — liveDiff marks the diff
	// as being re-read the moment it is asked for.
	changed := func(paths []string, more bool) tea.Cmd {
		cv.diffLive, cv.loading = false, false
		return m.handleEvent(mach, eventMsg(t, proto.EventWorktreeChanged,
			proto.WorktreeChanged{ProjectID: "r1", Worktree: wt, Paths: paths, More: more}))
	}

	// The open file is one of the ones that changed: it is re-read.
	if cmd := changed([]string{"a.go", "b.go"}, false); cmd == nil || !cv.diffLive {
		t.Fatalf("the open file changed: cmd %v, re-read %v", cmd != nil, cv.diffLive)
	}
	// Only other files changed: the branch is re-read, the diff is not. The
	// command left is the branch's, since the diff asked for nothing.
	if cmd := changed([]string{"c.go"}, false); cmd == nil || cv.diffLive {
		t.Fatalf("another file changed: cmd %v, re-read %v", cmd != nil, cv.diffLive)
	}
	// Too many paths to carry: the diff is re-read anyway rather than trusted.
	if cmd := changed([]string{"z.go"}, true); cmd == nil || !cv.diffLive {
		t.Fatalf("a truncated list: cmd %v, re-read %v", cmd != nil, cv.diffLive)
	}
	// A re-read already on its way is not asked for twice.
	cv.diffLive = true
	if m.handleEvent(mach, eventMsg(t, proto.EventWorktreeChanged,
		proto.WorktreeChanged{ProjectID: "r1", Worktree: wt, Paths: []string{"a.go"}})) == nil {
		t.Fatal("the branch is re-read even when the diff is already on its way")
	}
	cv.diffLive, cv.loading = false, false

	// Another worktree of the same project, and another project: nothing.
	for _, wc := range []proto.WorktreeChanged{
		{ProjectID: "r1", Worktree: "/src/api", Paths: []string{"a.go"}},
		{ProjectID: "r2", Worktree: wt, Paths: []string{"a.go"}},
		{ProjectID: "r1", Worktree: "", Paths: []string{"a.go"}},
	} {
		if cmd := m.handleEvent(mach, eventMsg(t, proto.EventWorktreeChanged, wc)); cmd != nil || cv.diffLive {
			t.Fatalf("%+v was applied", wc)
		}
	}
	// An event for another machine's view of the same project.
	other := newMachine("other", "other", "")
	other.state = stateOnline
	if cmd := m.handleEvent(other, eventMsg(t, proto.EventWorktreeChanged,
		proto.WorktreeChanged{ProjectID: "r1", Worktree: wt, Paths: []string{"a.go"}})); cmd != nil {
		t.Fatal("another machine's event was applied")
	}
	if m.handleEvent(mach, proto.Message{Event: proto.EventWorktreeChanged, Data: []byte("1")}) != nil {
		t.Fatal("a malformed event")
	}

	// A view that has read nothing yet has no worktree to match on.
	cv.data = nil
	if cmd := m.handleEvent(mach, eventMsg(t, proto.EventWorktreeChanged,
		proto.WorktreeChanged{ProjectID: "r1", Worktree: wt, Paths: []string{"a.go"}})); cmd != nil {
		t.Fatal("matched a view with nothing read")
	}
}

func TestA5PollBacksOffWhenWatched(t *testing.T) {
	watched, cv := a5WatchModel(t, proto.CapWorktreeWatch)
	// A server that watches, but not this worktree: keep polling fast.
	cv.data.Watched = false
	if got := watched.changesPollEvery(); got != changesPollEvery {
		t.Fatalf("an unwatched worktree polls every %s", got)
	}
	cv.data.Watched = true
	if got := watched.changesPollEvery(); got != changesBackstop {
		t.Fatalf("watched server polls every %s", got)
	}
	// An older server without the capability keeps the fast poll.
	old, oldCV := a5WatchModel(t)
	oldCV.data.Watched = true
	if got := old.changesPollEvery(); got != changesPollEvery {
		t.Fatalf("old server polls every %s", got)
	}
	// So does an offline machine, whose capabilities are unknown.
	offline, offCV := a5WatchModel(t, proto.CapWorktreeWatch)
	offCV.data.Watched = true
	offline.machines[0].c = nil
	if got := offline.changesPollEvery(); got != changesPollEvery {
		t.Fatalf("offline machine polls every %s", got)
	}
	// One unwatched machine on screen and they all keep polling.
	mixed, cvA := a5WatchModel(t, proto.CapWorktreeWatch)
	cvA.data.Watched = true
	cvB := &changesView{machine: "other", projectID: "r1", branch: "feat"}
	mixed.tabs = []*tab{{name: "1", root: &layoutNode{dir: splitRight, ratio: 0.5,
		a: &layoutNode{leaf: &leaf{id: 1, changes: cvA}},
		b: &layoutNode{leaf: &leaf{id: 2, changes: cvB}}}, focus: 1}}
	if got := mixed.changesPollEvery(); got != changesPollEvery {
		t.Fatalf("a mixed tab polls every %s", got)
	}
	// With no changes view up at all the interval is the plain one.
	none := a2Model()
	none.tabs = []*tab{{name: "1", root: &layoutNode{leaf: &leaf{id: 1}}, focus: 1}}
	if got := none.changesPollEvery(); got != changesPollEvery {
		t.Fatalf("no changes view polls every %s", got)
	}
}

// TestA5SplitsAreEachLive is the split case: two halves of a tab showing
// different branches each keep their own view, and an event about one
// worktree moves only the half that is showing it.
func TestA5SplitsAreEachLive(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].c = a2Client(proto.CapWorktreeWatch)
	m.rebuild()
	mainRow := m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "main"))]
	featRow := m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat"))]
	m.cursor = mainRow.id
	m.show(mainRow)
	m.split(splitRight, viewOf(featRow))

	leaves := m.tab().root.leaves()
	if len(leaves) != 2 || leaves[0].changes == nil || leaves[1].changes == nil {
		t.Fatalf("two branch splits: %+v", leaves)
	}
	if leaves[0].changes.branch != "main" || leaves[1].changes.branch != "feat" {
		t.Fatalf("branches %q and %q", leaves[0].changes.branch, leaves[1].changes.branch)
	}
	if leaves[0].changes == leaves[1].changes {
		t.Fatal("both halves share one view")
	}
	// Both are polled, not only the focused one.
	if m.pollChanges(); !m.changesPolling {
		t.Fatal("a tab of splits schedules no poll")
	}

	for _, l := range leaves {
		d := a2Changes("a.go")
		d.Worktree = "/src/api-" + l.changes.branch
		l.changes.receive(changesMsg{projectID: "r1", branch: l.changes.branch, data: d})
		l.changes.diffFile, l.changes.diffLive = "a.go", false
	}
	// An event about feat's worktree moves feat's half alone.
	if cmd := m.handleEvent(m.machines[0], eventMsg(t, proto.EventWorktreeChanged,
		proto.WorktreeChanged{ProjectID: "r1", Worktree: "/src/api-feat", Paths: []string{"a.go"}})); cmd == nil {
		t.Fatal("the event reached neither half")
	}
	if leaves[0].changes.diffLive {
		t.Fatal("main's half re-read its diff for feat's worktree")
	}
	if !leaves[1].changes.diffLive {
		t.Fatal("feat's half did not re-read its diff")
	}
}

// TestA5TabComingBackIsReRead covers what the splits above do not: only the
// tab on screen is polled, so one returned to must be read again rather than
// left showing what it had when it went away.
func TestA5TabComingBackIsReRead(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].c = a2Client(proto.CapWorktreeWatch)
	m.rebuild()
	mainRow := m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "main"))]
	featRow := m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat"))]
	m.cursor = mainRow.id
	m.show(mainRow)
	m.newTab(viewOf(featRow))
	if len(m.tabs) != 2 {
		t.Fatalf("%d tabs", len(m.tabs))
	}

	// Give the first tab's view something to be stale.
	first := m.tabs[0].root.leaves()[0]
	if first.changes == nil {
		t.Fatal("the first tab holds no changes view")
	}
	first.changes.receive(changesMsg{projectID: "r1", branch: "main", data: a2Changes("a.go")})
	first.changes.diffFile, first.changes.diffLive, first.changes.loading = "a.go", false, false

	// Going back to it re-reads the branch and the open diff.
	m.activeTab = 0
	m.syncView()
	if !first.changes.diffLive {
		t.Fatal("the open diff was not re-read when its tab came back")
	}
	// Syncing again without a tab change does not read it all over again.
	first.changes.diffLive, first.changes.loading = false, false
	m.syncView()
	if first.changes.diffLive {
		t.Fatal("re-read without changing tab")
	}
}

// TestA5SplitFillsTheNewHalfFromTheTree covers `v` (and `s`) on a branch
// while a tab is already showing something else: the branch belongs in the
// new half, beside what was there, not in place of it. Browsing the tree
// shows the row in a preview, and promoting that preview used to split the
// row away from itself and leave the new half empty to pick for.
func TestA5SplitFillsTheNewHalfFromTheTree(t *testing.T) {
	// No client: syncView would try to subscribe a pane on one that was
	// never connected, and block.
	m, _ := a1Fixture(t, false)
	m.rebuild()
	paneRow := m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p1"))]
	featRow := m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat"))]

	// A real tab showing the agent's pane.
	m.cursor = paneRow.id
	m.show(paneRow)
	if m.previewing || len(m.tabs) != 1 {
		t.Fatalf("a pane opens a tab: previewing=%v tabs=%d", m.previewing, len(m.tabs))
	}

	// Browsing to the branch previews it; the tab we were on is still there.
	m.cursor = featRow.id
	m.syncView()
	if !m.previewing {
		t.Fatal("a branch row is previewed")
	}
	if len(m.tabs) != 1 || m.tabs[0].root.leaves()[0].view.PaneID != "p1" {
		t.Fatalf("the pane's tab was lost: %d tabs", len(m.tabs))
	}

	// v puts the branch in the new half of that tab, beside the pane.
	m.split(splitRight, viewOf(featRow))
	if m.previewing {
		t.Fatal("still previewing after a split")
	}
	leaves := m.tab().root.leaves()
	if len(leaves) != 2 {
		t.Fatalf("%d halves", len(leaves))
	}
	if leaves[0].view.PaneID != "p1" {
		t.Fatalf("the first half no longer holds the pane: %+v", leaves[0].view)
	}
	if leaves[1].view.Kind != kindBranch || leaves[1].view.Branch != "feat" {
		t.Fatalf("the new half holds %+v, want feat's changes", leaves[1].view)
	}
	if leaves[1].pick {
		t.Fatal("the new half is waiting for a pick instead of showing the branch")
	}
	if m.tab().focus != leaves[1].id {
		t.Fatal("the new half does not take focus")
	}
	// syncView gives it its changes view, so it is live like any other.
	m.syncView()
	if leaves[1].changes == nil || leaves[1].changes.branch != "feat" {
		t.Fatalf("the new half has no changes view: %+v", leaves[1].changes)
	}
}

func TestA5SplitFromTheTreeEdgeCases(t *testing.T) {
	featOf := func(m *Model) row { return m.rows[indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat"))] }

	// With no tab to divide, the preview becomes one and the new half waits
	// for a pick — there is nothing else it could show.
	m, _ := a1Fixture(t, false)
	m.rebuild()
	m.tabs = nil
	m.cursor = featOf(m).id
	m.syncView()
	m.split(splitRight, viewOf(featOf(m)))
	leaves := m.tab().root.leaves()
	if len(leaves) != 2 || !leaves[1].pick {
		t.Fatalf("with no tabs: %d halves, pick=%v", len(leaves), len(leaves) > 1 && leaves[1].pick)
	}

	// Splitting onto what the focused half already shows still gives an
	// empty half: mirroring it would show one branch twice.
	m2, _ := a1Fixture(t, false)
	m2.rebuild()
	feat := featOf(m2)
	m2.cursor = feat.id
	m2.show(feat) // a real tab holding the branch
	m2.split(splitRight, viewOf(feat))
	ls := m2.tab().root.leaves()
	if len(ls) != 2 || !ls[1].pick {
		t.Fatalf("same row: %d halves, pick=%v", len(ls), len(ls) > 1 && ls[1].pick)
	}

	// s does the same downward.
	m3, _ := a1Fixture(t, false)
	m3.rebuild()
	paneRow := m3.rows[indexOfRow(m3.rows, paneNodeID(localMachine, "p1"))]
	m3.cursor = paneRow.id
	m3.show(paneRow)
	m3.cursor = featOf(m3).id
	m3.syncView()
	m3.split(splitDown, viewOf(featOf(m3)))
	ls = m3.tab().root.leaves()
	if len(ls) != 2 || ls[1].view.Branch != "feat" || ls[1].pick {
		t.Fatalf("s downward: %+v", ls[1].view)
	}
	if m3.tab().root.dir != splitDown {
		t.Fatalf("split direction %v", m3.tab().root.dir)
	}
}

// TestA5ChangesTab covers ctrl+b C: from the pane you are working in, the
// branch's changes open in a tab of their own, with no tree walking.
func TestA5ChangesTab(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.rebuild()
	// p1 runs on feat; open it in a tab and ask for its changes.
	paneRow := m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p1"))]
	m.cursor = paneRow.id
	m.show(paneRow)
	before := len(m.tabs)

	cmd, handled := m.layoutKey("C")
	if !handled {
		t.Fatal("ctrl+b C is not handled")
	}
	_ = cmd
	if len(m.tabs) != before+1 {
		t.Fatalf("%d tabs, want %d", len(m.tabs), before+1)
	}
	leaves := m.tab().root.leaves()
	if len(leaves) != 1 || leaves[0].view.Kind != kindBranch || leaves[0].view.Branch != "feat" {
		t.Fatalf("the new tab holds %+v", leaves[0].view)
	}
	if leaves[0].view.Row != branchNodeID(localMachine, "r1", "feat") {
		t.Fatalf("row %q", leaves[0].view.Row)
	}
	m.syncView()
	if leaves[0].changes == nil || leaves[0].changes.branch != "feat" {
		t.Fatal("the new tab has no changes view")
	}

	// Asking again goes to that tab rather than opening a second one.
	was := len(m.tabs)
	m.activeTab = 0
	if _, handled = m.layoutKey("C"); !handled {
		t.Fatal("second C not handled")
	}
	if len(m.tabs) != was {
		t.Fatalf("a second tab was opened: %d", len(m.tabs))
	}
	if m.tab().root.leaves()[0].view.Branch != "feat" {
		t.Fatal("C did not go to the tab already showing the branch")
	}

	// From the branch's own view it works the same, using the view's branch.
	if m.tab().root.leaves()[0].view.Kind != kindBranch {
		t.Fatal("expected to be on the branch tab")
	}
	if _, handled = m.layoutKey("C"); !handled || len(m.tabs) != was {
		t.Fatalf("C from the branch itself: handled=%v tabs=%d", handled, len(m.tabs))
	}
}

func TestA5ChangesTabWithoutABranch(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.rebuild()
	// p3 is a plain shell with no project or branch.
	paneRow := m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p3"))]
	m.cursor = paneRow.id
	m.show(paneRow)
	before := len(m.tabs)

	cmd, handled := m.layoutKey("C")
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd != nil)
	}
	if len(m.tabs) != before {
		t.Fatalf("a tab was opened for a pane with no branch: %d", len(m.tabs))
	}
	if !strings.Contains(m.flash, "not on a branch") || !m.flashIsErr {
		t.Fatalf("flash %q (err=%v)", m.flash, m.flashIsErr)
	}

	// An empty split says the same rather than opening anything.
	m.flash = ""
	m.tabs = []*tab{{name: "1", root: &layoutNode{leaf: &leaf{id: 1}}, focus: 1}}
	m.activeTab, m.previewing = 0, false
	if _, handled = m.layoutKey("C"); !handled || len(m.tabs) != 1 {
		t.Fatalf("empty split: handled=%v tabs=%d", handled, len(m.tabs))
	}
	if !strings.Contains(m.flash, "not on a branch") {
		t.Fatalf("flash %q", m.flash)
	}
}

// TestA5ChangingFiles covers the file list saying which files are being
// written: the diff marks lines, but the list has to name the file, or you
// cannot tell where an agent is working without opening each one.
func TestA5ChangingFiles(t *testing.T) {
	m := a2Model()
	m.width = 0 // pane area 80x24
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}

	// The first read marks nothing: everything is new, none of it is news.
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes("a.go", "b.go")})
	if cv.changingNow() != 0 {
		t.Fatalf("first read marked %d files", cv.changingNow())
	}
	if out := a2Plain(cv.render(*m, 80, 24)); strings.Contains(out, "changing") || strings.Contains(out, "▌") {
		t.Fatalf("first read:\n%s", out)
	}

	// An event names the file being written.
	cv.touch([]string{"b.go"})
	if !cv.changing("b.go") || cv.changing("a.go") || cv.changingNow() != 1 {
		t.Fatalf("after touch: %v", cv.touched)
	}
	out := a2Plain(cv.render(*m, 80, 24))
	if !strings.Contains(out, "· 1 changing") {
		t.Fatalf("header:\n%s", out)
	}
	for _, l := range cv.render(*m, 80, 24) {
		plain := ansi.Strip(l)
		if strings.Contains(plain, "b.go") && !strings.Contains(plain, "▌") {
			t.Fatalf("b.go is not marked: %q", plain)
		}
		if strings.Contains(plain, "a.go") && strings.Contains(plain, "▌") {
			t.Fatalf("a.go was marked: %q", plain)
		}
	}

	// It fades, and the header goes with it.
	cv.touched["b.go"] = time.Now().Add(-diffFreshFor - time.Millisecond)
	if cv.changing("b.go") || cv.changingNow() != 0 {
		t.Fatal("the mark did not fade")
	}
	if out = a2Plain(cv.render(*m, 80, 24)); strings.Contains(out, "changing") {
		t.Fatalf("faded:\n%s", out)
	}

	// A read whose lines moved marks the file, for a server sending no
	// events at all. a2Changes gives file i the counts i+1 / i.
	cv.touched = nil
	grew := a2Changes("a.go", "b.go")
	grew.Files[0].Added += 7
	if !cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: grew}) {
		t.Fatal("a poll that differs reports a change")
	}
	if !cv.changing("a.go") || cv.changing("b.go") {
		t.Fatalf("lines moved: %v", cv.touched)
	}
	// A new file counts as being written; one whose numbers held does not.
	cv.touched = nil
	cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: a2Changes("a.go", "b.go", "c.go")})
	if !cv.changing("c.go") {
		t.Fatal("a file that appeared is not marked")
	}
	// An identical poll returns early and marks nothing new.
	cv.touched = nil
	cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: a2Changes("a.go", "b.go", "c.go")})
	if cv.changingNow() != 0 {
		t.Fatalf("an identical poll marked %d", cv.changingNow())
	}
}

func TestA5TouchEdgeCases(t *testing.T) {
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	// Nothing to mark leaves the map alone rather than making one.
	cv.touch(nil)
	cv.touch([]string{})
	if cv.touched != nil {
		t.Fatalf("touched %v", cv.touched)
	}
	// changing and changingNow are safe before anything is read.
	if cv.changing("a.go") || cv.changingNow() != 0 {
		t.Fatal("changing on an empty view")
	}
	// Paths an event carries that are not in the list (a file git ignores,
	// say) are harmless: they mark nothing and are forgotten.
	cv.touch([]string{"build.log", "node_modules/x.js"})
	d := a2Changes("a.go")
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: d})
	if cv.changingNow() != 0 {
		t.Fatalf("paths outside the list counted: %d", cv.changingNow())
	}
	// Old entries are pruned rather than piling up.
	cv.touched["stale.go"] = time.Now().Add(-time.Hour)
	cv.touch([]string{"a.go"})
	if _, ok := cv.touched["stale.go"]; ok {
		t.Fatalf("a stale entry survived: %v", cv.touched)
	}
	if !cv.changing("a.go") {
		t.Fatal("a.go should be changing")
	}
}

func TestA5Darken(t *testing.T) {
	for _, tc := range []struct {
		in   lipgloss.Color
		f    float64
		want lipgloss.Color
	}{
		{"#FFFFFF", 0.5, "#7F7F7F"},
		{"#D29922", 0.26, "#362708"},
		{"#000000", 0.5, "#000000"},
		{"#FFFFFF", 1, "#FFFFFF"},
		{"#FFFFFF", 0, "#000000"},
		{"not a colour", 0.5, "not a colour"}, // left alone rather than guessed
		{"#12345", 0.5, "#12345"},             // too short
		{"#ZZZZZZ", 0.5, "#ZZZZZZ"},           // not hex
		{"", 0.5, ""},
	} {
		if got := darken(tc.in, tc.f); got != tc.want {
			t.Errorf("darken(%q, %v) = %q, want %q", tc.in, tc.f, got, tc.want)
		}
	}
}

func TestA5LiveWashIsReadableInEveryTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme("conch", "") })
	for _, th := range themes {
		applyTheme(th.name, "")
		bg := darken(th.warn, 0.26)
		if luminance(bg) >= luminance(th.warn) {
			t.Errorf("%s: the wash %q is not darker than warn %q", th.name, bg, th.warn)
		}
		// Dark enough that the light text textOn picks stays readable.
		if luminance(bg) > 0.45 {
			t.Errorf("%s: the wash %q is too bright to read light text on", th.name, bg)
		}
		if styleLive.GetBackground() != bg {
			t.Errorf("%s: styleLive background %v, want %v", th.name, styleLive.GetBackground(), bg)
		}
	}
}

// TestA5WashedRowKeepsItsWidth: the wash covers the row only if the row is
// exactly the pane's width, and only if nothing inside it resets the colour.
func TestA5WashedRowKeepsItsWidth(t *testing.T) {
	m := a2Model()
	m.width = 0
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes("a.go", "bbb.go")})
	cv.sel = 0
	cv.touch([]string{"bbb.go"}) // the unselected one is washed

	// filesTop is where the rows start, so this holds at a width that
	// truncates the names away too.
	for _, w := range []int{20, 40, 80, 200} {
		lines := cv.render(*m, w, 20)
		top := cv.filesTop(*m)
		if len(lines) < top+2 {
			t.Fatalf("width %d: only %d lines", w, len(lines))
		}
		for _, l := range lines[top : top+2] {
			if got := ansi.StringWidth(l); got != w {
				t.Fatalf("width %d: row %q is %d wide", w, ansi.Strip(l), got)
			}
		}
	}
	// The washed row carries no inner styling to reset the background: its
	// plain form and its rendered form differ only by the wrapping style.
	row := ""
	for _, l := range cv.render(*m, 60, 20) {
		if strings.Contains(ansi.Strip(l), "bbb.go") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("no row for bbb.go")
	}
	if strings.Count(row, "\x1b[0m") > 1 {
		t.Fatalf("the washed row resets colour part way: %q", strings.ReplaceAll(row, "\x1b", "^["))
	}
	if !strings.Contains(ansi.Strip(row), "▌") {
		t.Fatalf("the washed row lost its mark: %q", ansi.Strip(row))
	}
}

// TestA5WashedRowCarriesTheWash needs colour forced on: the test binary has
// no terminal, so lipgloss otherwise strips every escape and a washed row
// and a plain one come out identical. That is by design — a terminal without
// colour still gets the ▌ — but it means only a colour test can prove the
// wash is applied.
func TestA5WashedRowCarriesTheWash(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	m := a2Model()
	m.width = 0
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes("a.go", "b.go")})
	cv.sel = 0                 // the cursor is on a.go
	cv.touch([]string{"b.go"}) // b.go is the one being written

	bg := darken(themes[0].warn, 0.26) // #362708
	var v uint32
	fmt.Sscanf(string(bg)[1:], "%X", &v)
	want := fmt.Sprintf("48;2;%d;%d;%d", v>>16&0xFF, v>>8&0xFF, v&0xFF)

	lines := cv.render(*m, 60, 20)
	top := cv.filesTop(*m)
	cursorRow, liveRow := lines[top], lines[top+1]
	if !strings.Contains(ansi.Strip(liveRow), "b.go") {
		t.Fatalf("rows are not where expected: %q", ansi.Strip(liveRow))
	}
	if !strings.Contains(liveRow, want) {
		t.Fatalf("the row being written carries no wash (%s): %q", want, strings.ReplaceAll(liveRow, "\x1b", "^["))
	}
	if strings.Contains(cursorRow, want) {
		t.Fatalf("a row that is not changing was washed: %q", strings.ReplaceAll(cursorRow, "\x1b", "^["))
	}
	// Once it fades the wash goes with it.
	cv.touched["b.go"] = time.Now().Add(-diffFreshFor - time.Millisecond)
	if row := cv.render(*m, 60, 20)[top+1]; strings.Contains(row, want) {
		t.Fatalf("the wash outlived the change: %q", strings.ReplaceAll(row, "\x1b", "^["))
	}
}
