package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// a2VerifyModel is a project with a check configured and an agent that has
// just finished on the branch that is checked out.
func a2VerifyModel() (*Model, *machine) {
	m, _ := a2QueueModel()
	m.cfg.Verify.Commands = map[string]string{"r1": "go test ./..."}
	mach := m.machines[0]
	mach.c = a2Client() // a check needs a connection to start a terminal on
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "wip",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: time.Now().Add(-time.Minute)}},
	}
	return m, mach
}

func TestA2VerifyNothingRunsWithoutACommand(t *testing.T) {
	m, mach := a2VerifyModel()
	m.cfg.Verify.Commands = nil

	if cmd := m.verifyOnDone(mach.id, mach.panes[0]); cmd != nil {
		t.Fatal("a project with no check ran something anyway")
	}
	if cmd := m.startVerify(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("startVerify ran without a command")
	}
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyNone || text != "" {
		t.Fatalf("unchecked branch says %v %q", state, text)
	}
	// And the queue says nothing about checks.
	qv := &queueView{}
	out := a2Plain(qv.render(*m, 100, 20))
	for _, absent := range []string{"check passed", "check failed", "checking"} {
		if strings.Contains(out, absent) {
			t.Fatalf("queue says %q for a project with no check:\n%s", absent, out)
		}
	}
}

func TestA2VerifyRunsAndRecordsItsVerdict(t *testing.T) {
	m, mach := a2VerifyModel()
	// An agent finishing on a checked-out branch starts the check.
	if cmd := m.verifyOnDone(mach.id, mach.panes[0]); cmd == nil {
		t.Fatal("a finished agent didn't start the check")
	}
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyRunning || text != "checking…" {
		t.Fatalf("while it starts: %v %q", state, text)
	}
	// The pane it runs in reports back, then exits: the verdict is kept
	// even though conch closes an exited pane moments later.
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip",
		info: proto.PaneInfo{ID: "check1", State: proto.PaneRunning}})
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "check1", State: proto.PaneRunning})
	if state, _ := m.verifyOf(mach.id, "r1", "wip"); state != verifyRunning {
		t.Fatalf("running: %v", state)
	}
	m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 0})
	mach.panes = mach.panes[:1] // conch closes it
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyPassed || text != "check passed" {
		t.Fatalf("passed: %v %q", state, text)
	}
	// A failure keeps its exit code.
	m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 3})
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyFailed || !strings.Contains(text, "exit 3") {
		t.Fatalf("failed: %v %q", state, text)
	}
}

// A failed check is the clearest call for attention there is, so it sorts
// above agents that merely finished.
func TestA2VerifyFailurePromotesTheRow(t *testing.T) {
	m, mach := a2VerifyModel()
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip",
		info: proto.PaneInfo{ID: "check1"}})
	m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 1})

	// Another agent finished elsewhere, with no check against it.
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "p2", Name: "codex", State: proto.PaneRunning,
		ProjectID: "r1", Branch: "feat",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone, Since: time.Now().Add(-time.Hour)}})

	items := m.queueItems()
	if len(items) < 2 {
		t.Fatalf("want both rows: %+v", items)
	}
	if items[0].branch != "wip" || items[0].band != bandFailed {
		t.Fatalf("the failed check isn't first: %+v", items[0])
	}
	if items[1].band != bandDone {
		t.Fatalf("second row: %+v", items[1])
	}
	// Looked at from another row, the failed one carries the ✗ glyph.
	qv := &queueView{}
	qv.render(*m, 110, 20)
	qv.key(m, a2Key("down"))               // off the failed row, so its own glyph shows
	out := a2Plain(qv.render(*m, 150, 20)) // wide enough for the verdict in full
	for _, want := range []string{"✗", "check failed (exit 1)", "Codex finished"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\\n%s", want, out)
		}
	}
	// The row that passed says so instead.
	m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 0})
	if out := a2Plain((&queueView{}).render(*m, 150, 20)); !strings.Contains(out, "· check passed") {
		t.Fatalf("a passed check isn't shown:\\n%s", out)
	}
}

func TestA2VerifyKeyAsksAndRuns(t *testing.T) {
	m, mach := a2VerifyModel()
	qv := &queueView{}
	qv.render(*m, 100, 20)
	qv.sel = a2QueueAt(t, m, "wip")

	// With a command, v runs it.
	if _, cmd := qv.key(m, a2Key("v")); cmd == nil || !strings.Contains(m.flash, "checking wip") {
		t.Fatalf("v: cmd %v flash %q", cmd != nil, m.flash)
	}
	// While one is going, another isn't started.
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip",
		info: proto.PaneInfo{ID: "check1", State: proto.PaneRunning}})
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "check1", State: proto.PaneRunning})
	if cmd := m.startVerify(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("a second check started while one was running")
	}
	// Without a command, v asks for one instead of running nothing.
	m.cfg.Verify.Commands = nil
	if _, cmd := qv.key(m, a2Key("v")); cmd == nil {
		t.Fatal("v with no command should ask for one")
	}
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("v opened %T, want the command dialog", m.overlay)
	}
	// A branch with no worktree has nowhere to run, and says so.
	m.cfg.Verify.Commands = map[string]string{"r1": "go test ./..."}
	m.overlay = nil
	if cmd := m.startVerify(mach.id, "r1", "answering"); cmd != nil {
		t.Fatal("ran a check on a branch with no worktree")
	}
}

// A failing check outranks whatever put the row in the queue, not only an
// agent that finished: a dirty worktree whose check fails is still the
// first thing to look at.
func TestA2VerifyFailurePromotesAnyRow(t *testing.T) {
	m, mach := a2VerifyModel()
	mach.panes = nil // no agents at all; wip is here for its uncommitted files

	if it := m.queueItems()[0]; it.branch == "wip" || it.band == bandFailed {
		t.Fatalf("before the check the dirty branch is not first: %+v", it)
	}
	m.verifyRuns = map[string]verifyRun{
		verifyKey(mach.id, "r1", "wip"): {pane: "check1", done: true, exit: 2},
	}
	it := m.queueItems()[0]
	if it.branch != "wip" || it.band != bandFailed || !strings.Contains(it.checkText, "exit 2") {
		t.Fatalf("a failed check didn't come first: %+v", it)
	}
	// An agent waiting on an answer still outranks a failed check.
	mach.panes = []proto.PaneInfo{{ID: "p9", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "answering",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Since: time.Now()}}}
	if it := m.queueItems()[0]; it.branch != "answering" || it.band != bandWaiting {
		t.Fatalf("a waiting agent lost its place: %+v", it)
	}
}

// A check's terminal is its report: unlike a shell that exits, it is kept,
// and the verdict is said out loud. A check that passed used to vanish
// without a word.
func TestA2VerifyKeepsItsTerminalAndSaysSo(t *testing.T) {
	m, mach := a2VerifyModel()
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip",
		info: proto.PaneInfo{ID: "check1", State: proto.PaneRunning}})

	// Passing: reported as a check, so the pane is not closed, and said.
	if !m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 0}) {
		t.Fatal("a check's pane wasn't recognised as one")
	}
	if m.flash != "check passed on wip" || m.flashIsErr {
		t.Fatalf("passing flash %q (err %v)", m.flash, m.flashIsErr)
	}
	// Failing: says where the output is.
	if !m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 2}) {
		t.Fatal("a failing check's pane wasn't recognised")
	}
	if !strings.Contains(m.flash, "check failed on wip (exit 2)") || !strings.Contains(m.flash, "terminal") || !m.flashIsErr {
		t.Fatalf("failing flash %q", m.flash)
	}
	// Any other pane is none of its business, and is closed as before.
	if m.verifyExited(mach.id, proto.PaneInfo{ID: "p1", State: proto.PaneExited}) {
		t.Fatal("an unrelated pane was taken for a check")
	}
	// A check on another machine with the same pane ID is not confused for
	// this one.
	if m.verifyExited("box", proto.PaneInfo{ID: "check1", State: proto.PaneExited}) {
		t.Fatal("a check was matched across machines")
	}
}

// o goes to the check's terminal, which is where a failure's output is.
func TestA2VerifyOutputKey(t *testing.T) {
	m, mach := a2VerifyModel()
	m.rebuild()
	qv := &queueView{}
	qv.render(*m, 100, 20)
	qv.sel = a2QueueAt(t, m, "wip")
	qv.selKey = ""

	// Nothing has run yet: o says so rather than doing nothing.
	if _, cmd := qv.key(m, a2Key("o")); cmd != nil || !strings.Contains(m.flash, "nothing has checked wip yet") {
		t.Fatalf("o before any check: %q", m.flash)
	}
	// With a run, o opens its terminal.
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip",
		info: proto.PaneInfo{ID: "check1", State: proto.PaneRunning}})
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "check1", Name: "check · wip", State: proto.PaneRunning})
	m.verifyExited(mach.id, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 1})
	m.rebuild()
	// Opening a pane subscribes to it, and this fixture's client is never
	// read from; the check has already run, so drop it for the open.
	mach.c = nil
	// The command it returns subscribes to the pane; the view moves there
	// without needing a connection, so the test doesn't run it.
	if _, cmd := qv.key(m, a2Key("o")); cmd == nil {
		t.Fatal("o with a check's terminal did nothing")
	}
	if v := m.tab().focused().view; v.Kind != kindPane || v.PaneID != "check1" {
		t.Fatalf("o opened %+v, want the check's terminal", v)
	}
	// Once its terminal is closed, o says that instead of opening nothing.
	mach.panes = mach.panes[:len(mach.panes)-1]
	m.rebuild()
	if _, cmd := qv.key(m, a2Key("o")); cmd != nil || !strings.Contains(m.flash, "been closed") {
		t.Fatalf("o after the terminal went: %q", m.flash)
	}
}

// The dialog is how a check is named: it saves the command for the project
// and clears it again when emptied.
func TestA2VerifyCommandDialog(t *testing.T) {
	m, mach := a2VerifyModel()
	m.cfg.Verify.Commands = nil

	m.askVerifyCommand(mach.id, "r1", "wip")
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("overlay is %T", m.overlay)
	}
	if !strings.Contains(strings.Join(d.text, " "), "when its agent finishes") {
		t.Fatalf("dialog text %q", d.text)
	}
	// Naming a command saves it and runs it on the branch it was asked for.
	if cmd := d.submit(m, []string{"  make check  "}); cmd == nil {
		t.Fatal("naming a command neither saved nor ran anything")
	}
	if got := m.verifyCommand("r1"); got != "make check" {
		t.Fatalf("saved %q, want it trimmed", got)
	}
	if m.overlay != nil {
		t.Fatalf("the dialog stayed open: %T", m.overlay)
	}
	// Emptying it takes the check away, and says so.
	m.askVerifyCommand(mach.id, "r1", "wip")
	d = m.overlay.(*dialog)
	d.submit(m, []string{"   "})
	if _, ok := m.cfg.Verify.Commands["r1"]; ok {
		t.Fatal("an emptied command was kept")
	}
	if !strings.Contains(m.flash, "no check for api") {
		t.Fatalf("flash %q", m.flash)
	}
}

// The states a check can be in that aren't a plain pass or fail.
func TestA2VerifyOddStates(t *testing.T) {
	m, mach := a2VerifyModel()

	// A run that couldn't start says why, rather than pretending to run.
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip", err: "no such directory"})
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyBroken || text != "no such directory" {
		t.Fatalf("failed to start: %v %q", state, text)
	}
	// A pane conch hasn't heard of yet is still starting, not lost: its
	// pane.created may not have arrived.
	m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = verifyRun{pane: "notyet"}
	if state, _ := m.verifyOf(mach.id, "r1", "wip"); state != verifyRunning {
		t.Fatalf("a pane not yet known: %v", state)
	}
	// Closing a check's terminal before it finishes is a person doing it,
	// and the row says so rather than checking forever.
	m.verifyClosed(mach.id, "notyet")
	if state, text := m.verifyOf(mach.id, "r1", "wip"); state != verifyBroken || !strings.Contains(text, "closed before it finished") {
		t.Fatalf("terminal closed: %v %q", state, text)
	}
	// A check that had already finished is not rewritten by a later close.
	m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = verifyRun{pane: "p9", done: true}
	m.verifyClosed(mach.id, "p9")
	if state, _ := m.verifyOf(mach.id, "r1", "wip"); state != verifyPassed {
		t.Fatalf("a finished check was rewritten by its close: %v", state)
	}
	// A project that isn't there any more can't be checked.
	m.machines[0].projects = nil
	if cmd := m.startVerify(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("checked a project that is gone")
	}
}

// Only a finished agent on a branch of a project starts a check.
func TestA2VerifyOnDoneGuards(t *testing.T) {
	m, mach := a2VerifyModel()
	done := &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: time.Now()}
	for _, tc := range []struct {
		what string
		pane proto.PaneInfo
	}{
		{"no agent", proto.PaneInfo{ID: "x", ProjectID: "r1", Branch: "wip"}},
		{"still working", proto.PaneInfo{ID: "x", ProjectID: "r1", Branch: "wip",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}}},
		{"no branch", proto.PaneInfo{ID: "x", ProjectID: "r1", Agent: done}},
		{"no project", proto.PaneInfo{ID: "x", Branch: "wip", Agent: done}},
	} {
		if cmd := m.verifyOnDone(mach.id, tc.pane); cmd != nil {
			t.Fatalf("%s: started a check", tc.what)
		}
	}
	// The real thing does.
	if cmd := m.verifyOnDone(mach.id, proto.PaneInfo{ID: "x", ProjectID: "r1", Branch: "wip", Agent: done}); cmd == nil {
		t.Fatal("a finished agent on a checked project started nothing")
	}
}

// The whole path as it happens in use: an agent's update arrives, the check
// starts, its terminal exits, and conch keeps that terminal instead of
// closing it as it does every other pane that exits.
func TestA1VerifyThroughTheEventPath(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.cfg.Verify.Commands = map[string]string{"r1": "true"}
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "check9", Name: "check · feat", State: proto.PaneRunning})

	// p1 is on branch feat; it finishes.
	done := proto.PaneInfo{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: time.Now()}}
	cmd := m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneUpdated, done))
	msgs := a2Run(cmd)
	var started bool
	for _, msg := range msgs {
		if v, ok := msg.(verifyStartedMsg); ok {
			m.receiveVerifyStarted(v)
			started = true
		}
	}
	if !started {
		t.Fatalf("a finished agent started no check: %#v", msgs)
	}
	if state, _ := m.verifyOf(localMachine, "r1", "feat"); state != verifyRunning {
		t.Fatalf("after starting: %v", state)
	}

	// Its terminal exits. Every other pane would be closed; a check's is
	// kept, because its output is the only record of what happened.
	exit := proto.PaneInfo{ID: "check9", Name: "check · feat", State: proto.PaneExited, ExitCode: 0}
	m.machines[0].panes = append(m.machines[0].panes, exit)
	cmd = m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneExited, exit))
	for _, msg := range a2Run(cmd) {
		if _, ok := msg.(errMsg); ok {
			t.Fatalf("closing: %#v", msg)
		}
	}
	if n := peer.count(t, m.machines[0].c, proto.MethodPaneClose, "check9"); n != 0 {
		t.Fatalf("a check's terminal was closed %d times", n)
	}
	if state, text := m.verifyOf(localMachine, "r1", "feat"); state != verifyPassed || text != "check passed" {
		t.Fatalf("after it exited: %v %q", state, text)
	}
	if m.flash != "check passed on feat" {
		t.Fatalf("flash %q", m.flash)
	}
}

// A check that cannot even start its terminal says why, on the row.
func TestA2VerifyStartFails(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.cfg.Verify.Commands = map[string]string{"r1": "true"}
	peer.setError(proto.MethodPaneCreate, "too many panes")

	cmd := m.startVerify(localMachine, "r1", "feat")
	if cmd == nil {
		t.Fatal("no check started")
	}
	for _, msg := range a2Run(cmd) {
		if v, ok := msg.(verifyStartedMsg); ok {
			m.receiveVerifyStarted(v)
		}
	}
	state, text := m.verifyOf(localMachine, "r1", "feat")
	if state != verifyBroken || !strings.Contains(text, "too many panes") {
		t.Fatalf("a check that couldn't start says %v %q", state, text)
	}
}
