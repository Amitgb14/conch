package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// fixModel is a2VerifyModel with the check on wip already failed, its
// terminal still there to read, and a connection that answers.
func fixModel(t *testing.T, caps ...string) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, mach := a2VerifyModel()
	c, peer := a1FakeClient(t, caps...)
	mach.c = c
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 2})
	m.verifyRuns = map[string]verifyRun{verifyKey(mach.id, "r1", "wip"): {
		pane: "check1", done: true, exit: 2, sig: m.branchSig(mach.id, "r1", "wip")}}
	peer.setResult(proto.MethodPaneRead, proto.PaneReadResult{Lines: []string{
		"=== RUN TestThing", "--- FAIL: TestThing (0.00s)", "    thing_test.go:9: got 1, want 2", "FAIL", "", ""}})
	return m, mach, peer
}

func sentParams(t *testing.T, peer *a1Peer, method string, into any) {
	t.Helper()
	peer.mu.Lock()
	defer peer.mu.Unlock()
	for i := len(peer.msgs) - 1; i >= 0; i-- {
		if peer.msgs[i].Method == method {
			json.Unmarshal(peer.msgs[i].Params, into)
			return
		}
	}
	t.Fatalf("%s was never sent", method)
}

func TestFailureTail(t *testing.T) {
	if got := failureTail(nil); got != "" {
		t.Fatalf("nothing: %q", got)
	}
	if got := failureTail([]string{"", "  ", ""}); got != "" {
		t.Fatalf("blank lines: %q", got)
	}
	// Escape codes go, trailing blanks go, the order stays.
	got := failureTail([]string{"\x1b[31mFAIL\x1b[0m   ", "ok", "", ""})
	if got != "FAIL\nok" {
		t.Fatalf("plain: %q", got)
	}
	// Only the end of a long run.
	var many []string
	for i := range 500 {
		many = append(many, strings.Repeat("x", 10)+string(rune('a'+i%26)))
	}
	got = failureTail(many)
	if lines := strings.Split(got, "\n"); len(lines) != failureTailLines || lines[len(lines)-1] != many[499] {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	// Wide lines stop at the byte cap, cut at a line.
	wide := []string{strings.Repeat("a", 4000), strings.Repeat("b", 4000), "end"}
	if got := failureTail(wide); got != strings.Repeat("b", 4000)+"\nend" {
		t.Fatalf("byte cap kept %d bytes", len(got))
	}
	// One line bigger than the cap keeps its end, and stays valid text.
	huge := strings.Repeat("é", failureTailBytes) // two bytes each
	got = failureTail([]string{huge})
	if len(got) > failureTailBytes || !strings.HasSuffix(huge, got) || !json.Valid([]byte(`"`+got+`"`)) {
		t.Fatalf("one huge line: %d bytes", len(got))
	}
}

func TestFailurePrompt(t *testing.T) {
	p := failurePrompt("The project's check", "go test ./...", 2, "FAIL x")
	for _, want := range []string{"`go test ./...`, exit 2", "```\nFAIL x\n```", "run it again"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if p := failurePrompt("CI", "", 0, ""); !strings.HasPrefix(p, "CI failed on this branch. It printed nothing.") {
		t.Fatalf("bare: %s", p)
	}
}

func TestFixCheckSendsTheFailureToTheAgent(t *testing.T) {
	m, mach, peer := fixModel(t, proto.CapAgentPrompt)
	// The agent on wip has finished, and is told.
	cmd := m.fixCheck(mach.id, "r1", "wip")
	if cmd == nil {
		t.Fatalf("nothing sent: %q", m.flash)
	}
	msg, ok := cmd().(promptSentMsg)
	if !ok || msg.err != nil || msg.pane != "p1" {
		t.Fatalf("sent %#v", msg)
	}
	var pp proto.AgentPromptParams
	sentParams(t, peer, proto.MethodAgentPrompt, &pp)
	if pp.ID != "p1" || !strings.Contains(pp.Text, "thing_test.go:9: got 1, want 2") || !strings.Contains(pp.Text, "`go test ./...`, exit 2") {
		t.Fatalf("prompt %+v", pp)
	}
}

func TestFixCheckStartsAnAgentWhenNoneIsThere(t *testing.T) {
	m, mach, peer := fixModel(t, proto.CapAgentPrompt)
	mach.panes = mach.panes[1:] // the agent has gone
	m.cfg.Agents.Default = "codex"
	cmd := m.fixCheck(mach.id, "r1", "wip")
	if cmd == nil {
		t.Fatalf("nothing started: %q", m.flash)
	}
	if msg := cmd(); msg != flashMsg("started Codex on wip with the check's failure") {
		t.Fatalf("got %#v", msg)
	}
	var cp proto.PaneCreateParams
	sentParams(t, peer, proto.MethodPaneCreate, &cp)
	if cp.Agent != "codex" || cp.Cwd != "/src/api-wip" || !strings.Contains(cp.Prompt, "--- FAIL: TestThing") {
		t.Fatalf("started %+v", cp)
	}
}

func TestFixCheckRefusals(t *testing.T) {
	cases := map[string]struct {
		change func(m *Model, mach *machine)
		want   string
	}{
		"passed": {func(m *Model, mach *machine) {
			r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
			r.exit = 0
			m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
		}, "only a failed check"},
		"never checked": {func(m *Model, mach *machine) { m.verifyRuns = nil }, "only a failed check"},
		"offline":       {func(m *Model, mach *machine) { mach.c = nil }, "local is online"},
		"output closed": {func(m *Model, mach *machine) { mach.panes = mach.panes[:1] }, "has been closed"},
		"agent waiting": {func(m *Model, mach *machine) { mach.panes[0].Agent.State = proto.AgentBlocked }, "waiting for an answer"},
		"no worktree either": {func(m *Model, mach *machine) {
			mach.panes = mach.panes[1:]
			wts := m.machines[0].projects[0].Worktrees
			m.machines[0].projects[0].Worktrees = wts[:2] // wip's is gone
			r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
			r.sig = m.branchSig(mach.id, "r1", "wip") // still the failure it was
			m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
		}, "no agent on wip and no worktree"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m, mach, _ := fixModel(t, proto.CapAgentPrompt)
			tc.change(m, mach)
			if cmd := m.fixCheck(mach.id, "r1", "wip"); cmd != nil {
				t.Fatal("a refusal sent something")
			}
			if !strings.Contains(m.flash, tc.want) {
				t.Fatalf("flash %q, want %q", m.flash, tc.want)
			}
		})
	}
	// An old server cannot be prompted through.
	m, mach, _ := fixModel(t)
	if cmd := m.fixCheck(mach.id, "r1", "wip"); cmd != nil || !strings.Contains(m.flash, "predates prompting") {
		t.Fatalf("old server: %q", m.flash)
	}
}

func TestAutoFixIsCappedAndResetByAPass(t *testing.T) {
	m, mach, _ := fixModel(t, proto.CapAgentPrompt)
	exited := proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 2}

	// Off unless asked for.
	if cmd := m.autoFix(mach.id, exited); cmd != nil {
		t.Fatal("auto_fix 0 sent something")
	}
	m.cfg.Verify.AutoFix = 2
	for i := 1; i <= 2; i++ {
		if cmd := m.autoFix(mach.id, exited); cmd == nil {
			t.Fatalf("try %d sent nothing (%q)", i, m.flash)
		}
	}
	if cmd := m.autoFix(mach.id, exited); cmd != nil || !strings.Contains(m.flash, "tried 2 times") {
		t.Fatalf("past the cap: %q", m.flash)
	}
	// A pass resets the count.
	r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
	r.exit = 0
	m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
	m.autoFix(mach.id, exited)
	if m.verifyFixes[verifyKey(mach.id, "r1", "wip")] != 0 {
		t.Fatal("a pass didn't reset the count")
	}
	r.exit = 2
	m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r

	// Never to an agent that is busy or waiting, and never by starting one.
	for _, state := range []string{proto.AgentWorking, proto.AgentBlocked} {
		mach.panes[0].Agent.State = state
		if cmd := m.autoFix(mach.id, exited); cmd != nil {
			t.Fatalf("sent to an agent that is %s", state)
		}
	}
	mach.panes[0].Agent.State = proto.AgentDone
	agents := mach.panes
	mach.panes = mach.panes[1:]
	if cmd := m.autoFix(mach.id, exited); cmd != nil {
		t.Fatal("auto_fix started an agent nobody asked for")
	}
	mach.panes = agents
	// A pane that isn't a check does nothing.
	if cmd := m.autoFix(mach.id, proto.PaneInfo{ID: "p9", ExitCode: 1}); cmd != nil {
		t.Fatal("a stranger pane triggered a fix")
	}
}

func TestQueueFKeyFixes(t *testing.T) {
	m, _, _ := fixModel(t, proto.CapAgentPrompt)
	qv := &queueView{}
	m.overlay = nil
	qv.render(*m, 120, 30)
	// Move to the wip row, which failed.
	for i := 0; i < 20; i++ {
		if it, ok := itemAt(m.queueLinesFor(""), qv.sel); ok && it.branch == "wip" {
			break
		}
		qv.key(m, a2Key("down"))
	}
	if it, _ := itemAt(m.queueLinesFor(""), qv.sel); it.branch != "wip" {
		t.Fatalf("no wip row (on %q)", it.branch)
	}
	if _, cmd := qv.key(m, a2Key("f")); cmd == nil {
		t.Fatalf("f sent nothing: %q", m.flash)
	}
}

// ciModel has the shipped branch's open pull request failing CI.
func ciModel(t *testing.T, caps ...string) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, mach, peer := fixModel(t, caps...)
	m.verifyRuns = nil
	for i, b := range mach.projects[0].Branches {
		if b.Name == "shipped" {
			mach.projects[0].Branches[i].PR = &proto.PRInfo{Number: 7, State: "OPEN", Checks: "fail", URL: "https://example.invalid/pr/7"}
		}
	}
	return m, mach, peer
}

func TestQueueShowsCIFailures(t *testing.T) {
	m, _, _ := ciModel(t)
	var shipped *queueItem
	for _, it := range m.queueItems() {
		if it.branch == "shipped" {
			shipped = &it
		}
	}
	// Pushed with an open pull request is normally left out; failing CI
	// puts it back, as a failure.
	if shipped == nil || shipped.band != bandFailed || !shipped.ciFailed || shipped.detail != "CI failed on #7" {
		t.Fatalf("shipped: %+v", shipped)
	}
	// An agent's row on a branch whose CI failed says so too, once.
	m.machines[0].panes[0].Branch = "shipped"
	for _, it := range m.queueItems() {
		if it.branch == "shipped" {
			if it.paneID != "p1" || it.band != bandFailed || strings.Count(it.detail, "CI failed") != 1 {
				t.Fatalf("agent row: %+v", it)
			}
			return
		}
	}
	t.Fatal("no row for shipped")
}

func TestFixCISendsTheLog(t *testing.T) {
	m, mach, peer := ciModel(t, proto.CapAgentPrompt, proto.CapBranchCILog)
	mach.panes[0].Branch = "shipped"
	peer.setResult(proto.MethodBranchCILog, proto.BranchCILog{Workflow: "ci", URL: "https://x/runs/4", Log: "build\tgo test\t--- FAIL: TestY\n"})
	cmd := m.fixCI(mach.id, "r1", "shipped")
	if cmd == nil {
		t.Fatalf("nothing sent: %q", m.flash)
	}
	if msg, ok := cmd().(promptSentMsg); !ok || msg.err != nil {
		t.Fatalf("sent %#v", msg)
	}
	var lp proto.BranchCILogParams
	sentParams(t, peer, proto.MethodBranchCILog, &lp)
	var pp proto.AgentPromptParams
	sentParams(t, peer, proto.MethodAgentPrompt, &pp)
	if lp.Branch != "shipped" || !strings.HasPrefix(pp.Text, "CI (ci) failed") || !strings.Contains(pp.Text, "--- FAIL: TestY") || !strings.Contains(pp.Text, "https://x/runs/4") {
		t.Fatalf("asked %+v, sent %q", lp, pp.Text)
	}

	// The server's refusal is shown as it is.
	peer.setError(proto.MethodBranchCILog, "no failed GitHub Actions run on shipped")
	if msg := a2ErrText([]tea.Msg{m.fixCI(mach.id, "r1", "shipped")()}); !strings.Contains(msg, "no failed GitHub Actions run") {
		t.Fatalf("refused: %q", msg)
	}
	// An older server can't read CI.
	old, _, _ := ciModel(t, proto.CapAgentPrompt)
	if cmd := old.fixCI(localMachine, "r1", "shipped"); cmd != nil || !strings.Contains(old.flash, "predates reading CI logs") {
		t.Fatalf("old server: %q", old.flash)
	}
}

func TestQueueFOnARowWithNothingFailed(t *testing.T) {
	m, _, _ := fixModel(t, proto.CapAgentPrompt)
	m.verifyRuns = nil
	qv := &queueView{}
	qv.render(*m, 120, 30)
	if _, cmd := qv.key(m, a2Key("f")); cmd != nil || !strings.Contains(m.flash, "nothing has failed") {
		t.Fatalf("f on a quiet row: %q", m.flash)
	}
}

// A row says what you would open it to find out: what a waiting agent is
// asking, and how much is uncommitted on its branch.
func TestQueueRowsSayWhatHappened(t *testing.T) {
	m, qv := a2QueueModel()
	mach := m.machines[0]
	mach.panes[0].Agent.Message = "Claude needs your permission\n  to use Bash"
	for i, w := range mach.projects[0].Worktrees {
		if w.Branch == "feat" {
			mach.projects[0].Worktrees[i].Status = &proto.GitStatus{Files: 3, Added: 42, Deleted: 7}
		}
	}
	out := a2Plain(qv.render(*m, 200, 30))
	if !strings.Contains(out, "waiting for an answer: Claude needs your permission to use Bash") {
		t.Fatalf("the question isn't on its row:\n%s", out)
	}
	if !strings.Contains(out, "3 files +42 −7") {
		t.Fatalf("the work isn't on its row:\n%s", out)
	}
	// A dirty branch says its size once, not twice.
	if strings.Count(out, "3 files uncommitted") > 1 {
		t.Fatalf("doubled:\n%s", out)
	}
	// The filter finds a row by what its agent asked.
	qv.filter = "permission"
	if lines := m.queueLinesFor(qv.filter); len(lines) != 2 {
		t.Fatalf("filtering by the question: %d lines", len(lines))
	}
	// Narrow terminals still fit.
	for _, w := range []int{20, 39, 60} {
		for _, l := range qv.render(*m, w, 20) {
			if lw := ansi.StringWidth(l); lw > w {
				t.Fatalf("width %d: a line of %d: %q", w, lw, l)
			}
		}
	}
	if branchWorkClean := m.branchWork(mach.id, "r1", "main"); branchWorkClean != "" {
		t.Fatalf("a clean checkout says %q", branchWorkClean)
	}
	if m.branchWork(mach.id, "nope", "x") != "" || m.branchWork(mach.id, "r1", "") != "" {
		t.Fatal("work for nothing")
	}
}
