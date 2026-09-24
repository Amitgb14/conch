package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// a9Queue is a model with a few things waiting, across two projects.
func a9Queue(t *testing.T) *Model {
	t.Helper()
	m := a2Model()
	m.width, m.height = 100, 30
	now := time.Now()
	mach := m.machines[0]
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "r2", Name: "onenutri", Path: "/src/nutri", Git: true, Base: "main",
		Branches:  []proto.BranchInfo{{Name: "main"}, {Name: "onenutri/fix-login", BaseAhead: 2, Committed: now.Add(-time.Hour)}},
		Worktrees: []proto.WorktreeInfo{{Path: "/src/nutri", Branch: "main", Main: true}}})
	mach.panes = append(mach.panes, proto.PaneInfo{
		ID: "p7", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentBlocked, Since: now.Add(-5 * time.Minute)}})
	m.rebuild()
	return m
}

// The filter narrows the queue by anything the row shows, and says how much
// it is hiding.
func TestA9QueueFilter(t *testing.T) {
	m := a9Queue(t)
	all := m.queueItems()
	if len(all) < 2 {
		t.Fatalf("expected something to review, got %d", len(all))
	}
	for _, c := range []struct {
		filter string
		want   string // a branch that must survive
		gone   string // one that must not
	}{
		{"onenutri", "onenutri/fix-login", "feat"},
		{"codex", "feat", "onenutri/fix-login"},
		{"waiting", "feat", "onenutri/fix-login"},
		{"api", "feat", "onenutri/fix-login"}, // the project name
		{"ONENUTRI FIX", "onenutri/fix-login", "feat"},
	} {
		kept, hidden := filterQueue(all, c.filter)
		var names []string
		for _, it := range kept {
			names = append(names, it.branch)
		}
		joined := strings.Join(names, ",")
		if !strings.Contains(joined, c.want) || (c.gone != "" && strings.Contains(joined, c.gone)) {
			t.Errorf("filter %q kept %q", c.filter, joined)
		}
		if hidden != len(all)-len(kept) {
			t.Errorf("filter %q: hidden %d of %d kept %d", c.filter, hidden, len(all), len(kept))
		}
	}
	// Nothing typed keeps everything; nonsense keeps nothing.
	if kept, hidden := filterQueue(all, "   "); len(kept) != len(all) || hidden != 0 {
		t.Fatalf("an empty filter should keep everything: %d, %d hidden", len(kept), hidden)
	}
	if kept, hidden := filterQueue(all, "zzzz"); len(kept) != 0 || hidden != len(all) {
		t.Fatalf("nonsense kept %d", len(kept))
	}
}

// Typing the filter: / opens it, letters narrow, backspace goes back, enter
// keeps it, esc throws it away, and the list follows along.
func TestA9QueueFilterTyping(t *testing.T) {
	m := a9Queue(t)
	qv := &queueView{}
	qv.key(m, a2Key("/"))
	if !qv.typing {
		t.Fatal("/ should start the filter")
	}
	for _, r := range "onenutri" {
		qv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if qv.filter != "onenutri" {
		t.Fatalf("filter %q", qv.filter)
	}
	shown := m.queueLinesFor(qv.filter)
	if len(shown) == 0 {
		t.Fatal("the filter hid everything it should have kept")
	}
	for _, l := range shown {
		if l.header == "" && !strings.Contains(l.item.branch+l.item.project, "onenutri") {
			t.Fatalf("filter let through %q", l.item.branch)
		}
	}
	// A space is part of the filter, backspace takes it away again.
	qv.key(m, tea.KeyMsg{Type: tea.KeySpace})
	qv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if qv.filter != "onenutri f" {
		t.Fatalf("after space: %q", qv.filter)
	}
	qv.key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	qv.key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if qv.filter != "onenutri" {
		t.Fatalf("after backspace: %q", qv.filter)
	}
	// enter keeps it and stops typing; the header shows it.
	qv.key(m, a2Key("enter"))
	if qv.typing || qv.filter != "onenutri" {
		t.Fatalf("enter: typing=%v filter=%q", qv.typing, qv.filter)
	}
	out := a2Plain(qv.render(*m, m.width, m.height))
	if !strings.Contains(out, "/ onenutri") || !strings.Contains(out, "hidden by the filter") {
		t.Fatalf("the header should show the filter and what it hides:\n%s", out)
	}
	// esc clears the filter, and only then leaves the queue.
	if back, _ := qv.key(m, a2Key("esc")); back || qv.filter != "" {
		t.Fatalf("esc should clear first: back=%v filter=%q", back, qv.filter)
	}
	if back, _ := qv.key(m, a2Key("esc")); !back {
		t.Fatal("esc with no filter should leave the queue")
	}
	// A filter that matches nothing says so rather than looking empty.
	qv.filter = "zzzz"
	if out := a2Plain(qv.render(*m, m.width, m.height)); !strings.Contains(out, "Nothing matches zzzz") {
		t.Fatalf("no matches:\n%s", out)
	}
}

// What x puts aside now outlives the TUI, and is forgotten once the row it
// was about is gone.
func TestA9QueueDismissalsAreSaved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")
	m := a9Queue(t)
	m.statePath = path
	qv := &queueView{}
	list := m.queueLinesFor("")
	qv.onRow(list)
	it, ok := itemAt(list, qv.sel)
	if !ok {
		t.Fatal("nothing to dismiss")
	}
	_, cmd := qv.key(m, a2Key("x"))
	if cmd == nil {
		t.Fatal("dismissing should save the state")
	}
	a2Run(cmd)
	if _, gone := m.queueSeen[it.key()]; !gone {
		t.Fatal("the row was not dismissed")
	}
	// It is on disk, and comes back when the TUI is started again.
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "queue_dismissed") {
		t.Fatalf("not saved: %v %s", err, b)
	}
	st := loadUIState(path)
	if st.QueueDismissed[it.key()] != it.state() {
		t.Fatalf("read back %q", st.QueueDismissed[it.key()])
	}
	// An older ui.json without the field still loads.
	old := filepath.Join(dir, "old.json")
	os.WriteFile(old, []byte(`{"expanded":{"a":true},"sidebar_width":30}`), 0o600)
	if st := loadUIState(old); st.QueueDismissed == nil || !st.Expanded["a"] {
		t.Fatalf("an older state file: %+v", st)
	}
}

// Dismissals are forgotten for rows that no longer exist, but kept while
// their machine is away — its rows are not there to match against.
func TestA9PruneDismissals(t *testing.T) {
	items := []queueItem{
		{machine: "local", projectID: "r1", branch: "feat"},
		{machine: "local", projectID: "r1", branch: "other"},
	}
	seen := map[string]string{
		items[0].key():         "still here",
		"local|r1|deleted|":    "gone",
		"busybox|r9|whatever|": "away",
	}
	pruneQueueSeen(seen, items, map[string]bool{"local": true, "busybox": false})
	if _, ok := seen[items[0].key()]; !ok {
		t.Error("a live row's dismissal was dropped")
	}
	if _, ok := seen["local|r1|deleted|"]; ok {
		t.Error("a row that no longer exists should be forgotten")
	}
	if _, ok := seen["busybox|r9|whatever|"]; !ok {
		t.Error("an offline machine's dismissals should be kept")
	}
	pruneQueueSeen(nil, items, nil) // nothing dismissed: nothing to do
}

// A verdict is about the branch as it was: when the branch moves on, the
// check is out of date, and says so rather than claiming to have passed.
func TestA9CheckGoesStale(t *testing.T) {
	m := a9Queue(t)
	m.cfg.Verify.Commands = map[string]string{"r1": "go test ./..."} // keyed by project id
	key := verifyKey(localMachine, "r1", "feat")
	sig := m.branchSig(localMachine, "r1", "feat")
	if sig == "" {
		t.Fatal("a branch with a worktree should have a signature")
	}
	m.verifyRuns = map[string]verifyRun{key: {done: true, exit: 0, sig: sig}}
	if state, text := m.verifyOf(localMachine, "r1", "feat"); state != verifyPassed || text != "check passed" {
		t.Fatalf("fresh: %v %q", state, text)
	}

	// Somebody edits the worktree: the verdict is about what it was.
	proj := &m.machines[0].projects[0]
	for i := range proj.Worktrees {
		if proj.Worktrees[i].Branch == "feat" {
			proj.Worktrees[i].Status = &proto.GitStatus{Files: 3, Added: 40}
		}
	}
	state, text := m.verifyOf(localMachine, "r1", "feat")
	if state != verifyStale || text != "check out of date" {
		t.Fatalf("after an edit: %v %q", state, text)
	}
	// A failed check that went out of date says both, and no longer sorts
	// to the top as a failure.
	m.verifyRuns[key] = verifyRun{done: true, exit: 1, sig: sig}
	state, text = m.verifyOf(localMachine, "r1", "feat")
	if state != verifyStale || !strings.Contains(text, "out of date") || !strings.Contains(text, "exit 1") {
		t.Fatalf("stale failure: %v %q", state, text)
	}
	if band := failedFirst(bandDirty, state); band != bandDirty {
		t.Fatalf("a stale failure should stay where it was: %d", band)
	}
	// A run with no signature (an older TUI's, or one still going) is left
	// alone rather than called stale.
	m.verifyRuns[key] = verifyRun{done: true, exit: 0}
	if state, _ := m.verifyOf(localMachine, "r1", "feat"); state != verifyPassed {
		t.Fatalf("without a signature: %v", state)
	}
}

// A check that went out of date is run again once the branch has been quiet
// for a while — but not while an agent is still writing in it.
func TestA9RecheckWhenSettled(t *testing.T) {
	m := a9Queue(t)
	m.cfg.Verify.Commands = map[string]string{"r1": "go test ./..."} // keyed by project id
	c, _ := a1FakeClient(t, "pane.v1")
	m.machines[0].c = c
	key := verifyKey(localMachine, "r1", "feat")
	m.verifyRuns = map[string]verifyRun{key: {done: true, exit: 0, sig: "an older state"}}

	now := time.Now()
	// The first look only remembers where things stand.
	if cmd := m.recheckSettled(now); cmd != nil {
		t.Fatal("the first look should only take note")
	}
	// Still settling: nothing runs yet.
	if cmd := m.recheckSettled(now.Add(verifyResettle / 2)); cmd != nil {
		t.Fatal("it ran before the branch had settled")
	}
	// An agent still working on it holds the check back.
	m.machines[0].panes = append(m.machines[0].panes, proto.PaneInfo{
		ID: "p8", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}})
	if cmd := m.recheckSettled(now.Add(2 * verifyResettle)); cmd != nil {
		t.Fatal("it ran while an agent was still writing")
	}
	// Once the agent stops and the branch has been quiet, it runs.
	m.machines[0].panes[len(m.machines[0].panes)-1].Agent.State = proto.AgentIdle
	if cmd := m.recheckSettled(now.Add(2 * verifyResettle)); cmd == nil {
		t.Fatal("a settled branch with a stale check should be checked again")
	}
	if run := m.verifyRuns[key]; run.done || run.sig == "an older state" {
		t.Fatalf("the new run should be recorded: %+v", run)
	}
	// A branch that keeps moving keeps resetting the clock.
	m.verifyRuns[key] = verifyRun{done: true, exit: 0, sig: "older still"}
	m.recheckSettled(now)
	proj := &m.machines[0].projects[0]
	for i := range proj.Worktrees {
		if proj.Worktrees[i].Branch == "feat" {
			proj.Worktrees[i].Status = &proto.GitStatus{Files: 9}
		}
	}
	if cmd := m.recheckSettled(now.Add(2 * verifyResettle)); cmd != nil {
		t.Fatal("a branch that just changed should be given time to settle")
	}
	// And a project with no check command is never touched.
	m.cfg.Verify.Commands = nil
	if cmd := m.recheckSettled(now.Add(10 * verifyResettle)); cmd != nil {
		t.Fatal("no command, no check")
	}
}
