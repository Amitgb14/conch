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

// proj is a project with one branch whose open pull request's checks are
// in the state given; "" is a branch with no pull request at all.
func ciProject(checks string) proto.ProjectInfo {
	p := proto.ProjectInfo{ID: "r1", Name: "api", Branches: []proto.BranchInfo{{Name: "wip"}}}
	if checks != "" {
		p.Branches[0].PR = &proto.PRInfo{Number: 7, State: "OPEN", Checks: checks}
	}
	return p
}

// TestAutoFixCIOnTheTurnToRed: a pull request whose checks have just
// failed goes back to its branch's agent on the same terms as a check of
// conch's own — and only on the turn, so a poll that says "still red"
// does not ask again.
func TestAutoFixCIOnTheTurnToRed(t *testing.T) {
	m, mach, _ := fixModel(t, proto.CapAgentPrompt, proto.CapBranchCILog)

	// Off unless asked for.
	if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd != nil {
		t.Fatal("auto_fix 0 sent something")
	}
	m.cfg.Verify.AutoFix = 2

	// Pending to failed is the turn: it is sent, once.
	if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd == nil {
		t.Fatalf("the turn to red sent nothing (%q)", m.flash)
	}
	if cmd := m.autoFixCI(mach.id, ciProject("fail"), ciProject("fail")); cmd != nil {
		t.Fatal("still red asked again")
	}
	// A branch with no pull request, and one whose checks pass, say
	// nothing.
	for _, c := range []string{"", "pass", "pending"} {
		if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject(c)); cmd != nil {
			t.Fatalf("checks %q sent something", c)
		}
	}

	// The cap is shared with conch's own check, and counts down.
	key := mach.id + "|r1|wip"
	m.verifyFixes[key] = 1
	if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd == nil {
		t.Fatalf("the second try sent nothing (%q)", m.flash)
	}
	if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd != nil ||
		!strings.Contains(m.flash, "tried 2 times") {
		t.Fatalf("past the cap: %q", m.flash)
	}
	// Green clears it, so a branch that goes red again gets the whole
	// allowance rather than none.
	m.autoFixCI(mach.id, ciProject("fail"), ciProject("pass"))
	if n := m.verifyFixes[key]; n != 0 {
		t.Fatalf("a pass left the count at %d", n)
	}

	// Never to an agent that is busy or waiting, and never by starting
	// one where there is none.
	for _, state := range []string{proto.AgentWorking, proto.AgentBlocked} {
		mach.panes[0].Agent.State = state
		if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd != nil {
			t.Fatalf("sent to an agent that is %s", state)
		}
	}
	mach.panes[0].Agent.State = proto.AgentDone
	panes := mach.panes
	mach.panes = nil
	if cmd := m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")); cmd != nil {
		t.Fatal("started an agent nobody asked for")
	}
	mach.panes = panes
}

// A project conch is seeing for the first time brings whatever state it
// is in, which conch did not watch happen: four red pull requests on a
// machine conch has just reached are the review queue's business, not
// four prompts.
func TestAutoFixCINotOnTheFirstSight(t *testing.T) {
	m, mach, _ := fixModel(t, proto.CapAgentPrompt, proto.CapBranchCILog)
	m.cfg.Verify.AutoFix = 2
	if cmd := m.autoFixCI(mach.id, proto.ProjectInfo{ID: "r1"}, ciProject("fail")); cmd == nil {
		t.Fatal("this test needs the turn to fire when the branch was known")
	}
	m.verifyFixes = map[string]int{}

	// What the event does with a project it has never seen: nothing.
	mach.projects = nil
	before := len(m.verifyFixes)
	m.handleEvent(mach, eventMsg(t, proto.EventProjectUpdated, ciProject("fail")))
	if len(m.verifyFixes) != before {
		t.Fatalf("a project arriving red was acted on: %v", m.verifyFixes)
	}
	// And once it is known, a turn to red is.
	mach.projects = []proto.ProjectInfo{ciProject("pending")}
	m.handleEvent(mach, eventMsg(t, proto.EventProjectUpdated, ciProject("fail")))
	if m.verifyFixes[mach.id+"|r1|wip"] != 1 {
		t.Fatalf("the turn was missed: %v", m.verifyFixes)
	}
}

// greenProject is ciProject with the pull request's other fields set, so
// a test can make it a draft or have changes requested on it.
func greenProject(checks string, set func(*proto.PRInfo)) proto.ProjectInfo {
	p := ciProject(checks)
	if p.Branches[0].PR != nil && set != nil {
		set(p.Branches[0].PR)
	}
	return p
}

// autoMergeModel is fixModel with auto_merge on, a server that merges
// pull requests, the agent finished and the project's own check passed —
// nothing holding a green pull request back.
func autoMergeModel(t *testing.T, caps ...string) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, mach, peer := fixModel(t, append([]string{proto.CapAgentPrompt, proto.CapBranchCILog}, caps...)...)
	mach.panes[0].Agent.State = proto.AgentDone
	r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
	r.exit = 0
	m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
	m.cfg.Verify.AutoMerge = true
	peer.setResult(proto.MethodBranchPRMerge, proto.BranchPRMergeResult{Method: "merge"})
	return m, mach, peer
}

// mergedPR is what the server was asked to merge, or nil when nothing was.
func mergedPR(peer *a1Peer) *proto.BranchPRMergeParams {
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodBranchMerge {
			panic("merged into a local checkout")
		}
		if msg.Method == proto.MethodBranchPRMerge {
			var mp proto.BranchPRMergeParams
			json.Unmarshal(msg.Params, &mp)
			return &mp
		}
	}
	return nil
}

// TestAutoMergeOnTheTurnToGreen: the other half of auto_fix, and the
// merge is GitHub's — a local one left the pull request open, and the
// base diverged from GitHub's once somebody merged it there.
func TestAutoMergeOnTheTurnToGreen(t *testing.T) {
	m, mach, peer := autoMergeModel(t, proto.CapPRMerge)

	// Off unless asked for.
	m.cfg.Verify.AutoMerge = false
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), ciProject("pass")); cmd != nil {
		t.Fatal("auto_merge off merged something")
	}
	m.cfg.Verify.AutoMerge = true

	// Pending to green is the turn, and it merges the pull request at the
	// commit whose checks passed.
	now := greenProject("pass", func(pr *proto.PRInfo) { pr.Head = "abc123" })
	msgs := runBatch(m.autoMergeCI(mach.id, ciProject("pending"), now))
	if !strings.Contains(m.flash, "#7 is green: merging it on GitHub") {
		t.Fatalf("flash %q", m.flash)
	}
	mp := mergedPR(peer)
	if mp == nil || mp.ProjectID != "r1" || mp.Branch != "wip" || mp.Number != 7 || mp.Head != "abc123" {
		t.Fatalf("merged %+v", mp)
	}
	// Still green while it merges, and after: not merged twice.
	if cmd := m.autoMergeCI(mach.id, now, now); cmd != nil {
		t.Fatal("still green merged again while merging")
	}
	if len(msgs) != 1 {
		t.Fatalf("messages %v", msgs)
	}
	next, _ := m.Update(msgs[0])
	*m = next.(Model)
	if !strings.Contains(m.flash, "merged #7 on GitHub (merge)") || len(m.autoMerges) != 0 {
		t.Fatalf("done: flash %q, waiting %v", m.flash, m.autoMerges)
	}
	if cmd := m.autoMergeCI(mach.id, now, now); cmd != nil {
		t.Fatal("still green merged again")
	}
	for _, c := range []string{"", "fail", "pending"} {
		if cmd := m.autoMergeCI(mach.id, ciProject("pending"), ciProject(c)); cmd != nil {
			t.Fatalf("checks %q merged", c)
		}
	}
	// Green when conch first saw it is not a turn conch watched.
	if cmd := m.autoMergeCI(mach.id, ciProject("pass"), ciProject("pass")); cmd != nil || len(m.autoMerges) != 0 {
		t.Fatal("merged a pull request that was green before conch looked")
	}
	// The base does not merge into itself.
	base := ciProject("pass")
	base.Base, base.Branches[0].Name = "wip", "wip"
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), base); cmd != nil {
		t.Fatal("merged the base")
	}
}

// TestAutoMergeHolds: everything it holds back for, it says out loud — a
// merge that does not happen is as surprising as one that does.
func TestAutoMergeHolds(t *testing.T) {
	m, mach, peer := autoMergeModel(t, proto.CapPRMerge)
	for _, c := range []struct {
		name, want string
		now        proto.ProjectInfo
		setup      func()
	}{
		{"draft", "is a draft", greenProject("pass", func(pr *proto.PRInfo) { pr.Draft = true }), nil},
		{"changes requested", "asked for changes",
			greenProject("pass", func(pr *proto.PRInfo) { pr.Review = "CHANGES_REQUESTED" }), nil},
		{"conflicts", "#7 conflicts with its base; rebase it",
			greenProject("pass", func(pr *proto.PRInfo) { pr.Conflicting = true }), nil},
		{"conflicts with a named base", "#7 conflicts with main; rebase it", func() proto.ProjectInfo {
			p := greenProject("pass", func(pr *proto.PRInfo) { pr.Conflicting = true })
			p.Base = "main"
			return p
		}(), nil},
		{"the project's own check failed", "this project's own check failed", ciProject("pass"), func() {
			r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
			r.exit = 2
			m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
		}},
		{"an unresolved review thread", "1 review thread unresolved on #7", ciProject("pass"), func() {
			r := m.verifyRuns[verifyKey(mach.id, "r1", "wip")]
			r.exit = 0
			m.verifyRuns[verifyKey(mach.id, "r1", "wip")] = r
			mach.reviews = []proto.ReviewComment{
				// conch's own note on the diff does not hold a branch:
				// it is the person's working note, not somebody waiting.
				{ProjectID: "r1", Branch: "wip", File: "a.go", Note: "mine"},
				{ProjectID: "r1", Branch: "wip", File: "b.go", Note: "theirs", Source: proto.ReviewGitHub},
			}
		}},
		{"its agent is still working", "still at it", ciProject("pass"), func() {
			mach.reviews = nil
			mach.panes[0].Agent.State = proto.AgentWorking
		}},
	} {
		m.flash, m.autoMerges = "", nil
		if c.setup != nil {
			c.setup()
		}
		if cmd := m.autoMergeCI(mach.id, ciProject("pending"), c.now); cmd != nil {
			t.Errorf("%s: merged anyway", c.name)
		}
		if !strings.Contains(m.flash, c.want) {
			t.Errorf("%s: said %q, want %q in it", c.name, m.flash, c.want)
		}
		// Said once: the next poll, still held, says nothing more.
		m.flash = ""
		if m.autoMergeCI(mach.id, c.now, c.now); m.flash != "" {
			t.Errorf("%s: said it again: %q", c.name, m.flash)
		}
	}
	if mp := mergedPR(peer); mp != nil {
		t.Fatalf("merged %+v", mp)
	}
}

// TestAutoMergeAfterAHold: held back is not given up. A green pull
// request whose agent was still working merges once the agent stops —
// which is the commonest case, since the agent that pushed is often still
// writing its summary when CI turns green — without waiting for the pull
// request to change, which may never happen.
func TestAutoMergeAfterAHold(t *testing.T) {
	m, mach, peer := autoMergeModel(t, proto.CapPRMerge)
	green := ciProject("pass")
	mach.projects = []proto.ProjectInfo{green}
	mach.panes[0].Agent.State = proto.AgentWorking
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), green); cmd != nil || !strings.Contains(m.flash, "still at it") {
		t.Fatalf("not held: %q", m.flash)
	}

	// The agent finishes: the pane event is enough.
	done := mach.panes[0]
	done.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentDone}
	runBatch(m.handleEvent(mach, eventMsg(t, proto.EventPaneUpdated, done)))
	if mp := mergedPR(peer); mp == nil || mp.Number != 7 {
		t.Fatalf("not merged once the agent finished: %+v (%q)", mp, m.flash)
	}
}

// TestAutoMergeAfterAThreadResolved: the same for a review thread
// resolved on GitHub, which arrives as the review list changing.
func TestAutoMergeAfterAThreadResolved(t *testing.T) {
	m, mach, peer := autoMergeModel(t, proto.CapPRMerge, proto.CapReviewComments)
	green := ciProject("pass")
	mach.projects = []proto.ProjectInfo{green}
	mach.reviews = []proto.ReviewComment{{ProjectID: "r1", Branch: "wip", File: "b.go", Note: "theirs", Source: proto.ReviewGitHub}}
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), green); cmd != nil {
		t.Fatal("merged over an unresolved thread")
	}
	runBatch(m.handleEvent(mach, eventMsg(t, proto.EventReviewChanged, proto.ReviewList{})))
	if mp := mergedPR(peer); mp == nil {
		t.Fatalf("not merged once the thread was resolved (%q)", m.flash)
	}
}

// TestAutoMergeWaitEnds: a pull request that goes red, pending, or is
// merged by somebody else stops waiting; one that is green again after a
// push starts again.
func TestAutoMergeWaitEnds(t *testing.T) {
	m, mach, _ := autoMergeModel(t, proto.CapPRMerge)
	mach.panes[0].Agent.State = proto.AgentWorking
	m.autoMergeCI(mach.id, ciProject("pending"), ciProject("pass"))
	if len(m.autoMerges) != 1 {
		t.Fatalf("not waiting: %v", m.autoMerges)
	}
	for _, now := range []proto.ProjectInfo{
		ciProject("pending"), ciProject("fail"), ciProject(""),
		greenProject("pass", func(pr *proto.PRInfo) { pr.State = "MERGED" }),
		{ID: "r1", Name: "api"}, // the branch is gone
	} {
		m.autoMerges = map[string]*autoMergeWait{mach.id + "|r1|wip": {held: "x"}}
		m.autoMergeCI(mach.id, ciProject("pass"), now)
		if len(m.autoMerges) != 0 {
			t.Errorf("%+v: still waiting", now.Branches)
		}
	}
	// Another project's wait is its own.
	m.autoMerges = map[string]*autoMergeWait{mach.id + "|r2|wip": {held: "x"}}
	m.autoMergeCI(mach.id, ciProject("pass"), ciProject("pending"))
	if len(m.autoMerges) != 1 {
		t.Fatal("dropped another project's wait")
	}
	// Retrying with auto_merge turned off since does nothing.
	m.cfg.Verify.AutoMerge = false
	if cmd := m.autoMergeRetry(mach.id); cmd != nil {
		t.Fatal("retried with auto_merge off")
	}
}

// TestAutoMergeRefused: an older server, which can only merge into a
// checkout, is not asked; GitHub refusing is said once, and not tried
// again until the pull request is green again.
func TestAutoMergeRefused(t *testing.T) {
	m, mach, peer := autoMergeModel(t) // no CapPRMerge
	green := ciProject("pass")
	mach.projects = []proto.ProjectInfo{green}
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), green); cmd != nil || !strings.Contains(m.flash, "predates merging pull requests") {
		t.Fatalf("an old server: %q", m.flash)
	}
	if len(m.autoMerges) != 1 {
		t.Fatal("an old server gave up rather than waiting for an update")
	}
	if mp := mergedPR(peer); mp != nil {
		t.Fatalf("asked an old server: %+v", mp)
	}

	m, mach, peer = autoMergeModel(t, proto.CapPRMerge)
	peer.setError(proto.MethodBranchPRMerge, "gh pr merge: Pull request o/r#7 is not mergeable")
	msgs := runBatch(m.autoMergeCI(mach.id, ciProject("pending"), green))
	if len(msgs) != 1 {
		t.Fatalf("messages %v", msgs)
	}
	next, _ := m.Update(msgs[0])
	*m = next.(Model)
	if !strings.Contains(m.flash, "#7 is green but GitHub would not merge it") || !strings.Contains(m.flash, "not mergeable") {
		t.Fatalf("flash %q", m.flash)
	}
	if len(m.autoMerges) != 0 || m.autoMergeCI(mach.id, green, green) != nil {
		t.Fatal("tried again without the pull request changing")
	}
	// Green again after a push: tried again.
	if cmd := m.autoMergeCI(mach.id, ciProject("pending"), green); cmd == nil {
		t.Fatal("green again was not tried")
	}
}
