package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// reviewModel is a branch (wip) whose agent has just finished, a reviewer
// configured, and a connection that answers.
func reviewModel(t *testing.T, caps ...string) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, mach := a2VerifyModel()
	c, peer := a1FakeClient(t, caps...)
	mach.c = c
	m.cfg.Verify.Reviewer = "codex"
	return m, mach, peer
}

func TestReviewerStartsWhenTheWriterFinishes(t *testing.T) {
	m, mach, peer := reviewModel(t)
	m.cfg.Verify.Commands = nil // no check: the review starts at once
	cmd := m.verifyOnDone(mach.id, mach.panes[0])
	if cmd == nil {
		t.Fatalf("no reviewer started: %q", m.flash)
	}
	cmd()
	var cp proto.PaneCreateParams
	sentParams(t, peer, proto.MethodPaneCreate, &cp)
	if cp.Agent != "codex" || cp.Name != "review · wip" || cp.Cwd != "/src/api-wip" {
		t.Fatalf("started %+v", cp)
	}
	for _, want := range []string{"branch wip, against main", "Do not edit", "git diff main...HEAD", "final message"} {
		if !strings.Contains(cp.Prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, cp.Prompt)
		}
	}
	// The same state of the branch is not reviewed twice…
	if cmd := m.verifyOnDone(mach.id, mach.panes[0]); cmd != nil {
		t.Fatal("reviewed the same state twice")
	}
	// …and a reviewer finishing starts nothing at all.
	reviewer := proto.PaneInfo{ID: "r9", Name: "review · wip", State: proto.PaneRunning, ProjectID: "r1", Branch: "wip",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone}}
	mach.panes = append(mach.panes, reviewer)
	m.reviewSigs = nil
	if cmd := m.verifyOnDone(mach.id, reviewer); cmd != nil {
		t.Fatal("a reviewer finishing started something")
	}
	// While one is reading the branch, another isn't started.
	mach.panes[len(mach.panes)-1].Agent.State = proto.AgentWorking
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("a second reviewer started")
	}
	// Asking, or failed: left for somebody to look at, and not replaced.
	mach.panes[len(mach.panes)-1].Agent.State = proto.AgentBlocked
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("replaced a reviewer that is asking")
	}
	mach.panes[len(mach.panes)-1].Agent = &proto.AgentStatus{Name: "codex", State: proto.AgentDone, Failed: true}
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("replaced a reviewer that failed")
	}
}

// A finished reviewer on a branch that has changed since is closed as
// the next review starts, rather than left beside it.
func TestReviewerReplacesTheLastOne(t *testing.T) {
	m, mach, peer := reviewModel(t)
	m.cfg.Verify.Commands = nil
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "r9", Name: "review · wip", State: proto.PaneRunning, ProjectID: "r1", Branch: "wip",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone}})
	cmd := m.startReview(mach.id, "r1", "wip")
	if cmd == nil {
		t.Fatalf("no new review: %q", m.flash)
	}
	cmd()
	var ref proto.PaneRef
	sentParams(t, peer, proto.MethodPaneClose, &ref)
	if ref.ID != "r9" {
		t.Fatalf("closed %q, want the last reviewer r9", ref.ID)
	}
	ms := peer.methods()
	ci, ni := slices.Index(ms, proto.MethodPaneClose), slices.Index(ms, proto.MethodPaneCreate)
	if ci < 0 || ni < 0 || ci > ni {
		t.Fatalf("closed after starting the new one: %v", ms)
	}
}

// A reviewer starts at the size it will be shown at — the TUI's pane area
// — so an agent that draws inline is not rewrapped when it is opened.
func TestReviewerStartsAtThePaneSize(t *testing.T) {
	m, mach, peer := reviewModel(t)
	m.cfg.Verify.Commands = nil
	for _, sz := range [][2]int{{240, 60}, {40, 12}} {
		m.width, m.height = sz[0], sz[1]
		m.reviewSigs = nil
		mach.panes = mach.panes[:1]
		cmd := m.verifyOnDone(mach.id, mach.panes[0])
		if cmd == nil {
			t.Fatalf("no reviewer started at %dx%d: %q", sz[0], sz[1], m.flash)
		}
		cmd()
		var cp proto.PaneCreateParams
		sentParams(t, peer, proto.MethodPaneCreate, &cp)
		cols, rows := m.paneArea()
		if cp.Cols != cols || cp.Rows != rows || cols >= sz[0] || cols < 1 || rows < 1 {
			t.Fatalf("screen %dx%d: started at %d×%d, pane area is %d×%d", sz[0], sz[1], cp.Cols, cp.Rows, cols, rows)
		}
	}
	// Before the TUI has a size: 120×40, not the server's 80×24.
	m.width, m.height = 0, 0
	m.reviewSigs = nil
	mach.panes = mach.panes[:1]
	cmd := m.verifyOnDone(mach.id, mach.panes[0])
	if cmd == nil {
		t.Fatal("no reviewer started with no size known")
	}
	cmd()
	var cp proto.PaneCreateParams
	sentParams(t, peer, proto.MethodPaneCreate, &cp)
	if cp.Cols != 120 || cp.Rows != 40 {
		t.Fatalf("started at %d×%d with no size known, want 120×40", cp.Cols, cp.Rows)
	}
}

func TestReviewerWaitsForTheCheck(t *testing.T) {
	m, mach, peer := reviewModel(t)
	// With a check, the check runs first and the review waits for it.
	if cmd := m.verifyOnDone(mach.id, mach.panes[0]); cmd == nil {
		t.Fatal("no check started")
	}
	m.receiveVerifyStarted(verifyStartedMsg{machine: mach.id, projectID: "r1", branch: "wip", info: proto.PaneInfo{ID: "check1"}})
	failed := proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 1}
	m.verifyExited(mach.id, failed)
	if cmd := m.reviewAfterCheck(mach.id, failed); cmd != nil {
		t.Fatal("a failed check was reviewed")
	}
	passed := proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 0}
	m.verifyExited(mach.id, passed)
	cmd := m.reviewAfterCheck(mach.id, passed)
	if cmd == nil {
		t.Fatalf("a passed check wasn't reviewed: %q", m.flash)
	}
	cmd()
	var cp proto.PaneCreateParams
	sentParams(t, peer, proto.MethodPaneCreate, &cp)
	if cp.Agent != "codex" {
		t.Fatalf("started %+v", cp)
	}
}

func TestReviewerRefusals(t *testing.T) {
	m, mach, _ := reviewModel(t)
	m.cfg.Verify.Commands = nil
	// Nobody named: nothing.
	m.cfg.Verify.Reviewer = " "
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("reviewed with no reviewer")
	}
	m.cfg.Verify.Reviewer = "codex"
	// Not installed there: said, not started.
	mach.available = map[string]proto.AgentAvailability{"codex": {Installed: false}}
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil || !strings.Contains(m.flash, "Codex is not installed") {
		t.Fatalf("missing reviewer: %q", m.flash)
	}
	mach.available = nil
	// The base branch, an unknown branch, a branch with no worktree.
	for _, b := range []string{"main", "", "quiet"} {
		if cmd := m.startReview(mach.id, "r1", b); cmd != nil {
			t.Fatalf("reviewed %q", b)
		}
	}
	// Offline.
	mach.c = nil
	if cmd := m.startReview(mach.id, "r1", "wip"); cmd != nil {
		t.Fatal("reviewed offline")
	}
}

func TestReviewGoesToTheWriter(t *testing.T) {
	m, mach, peer := reviewModel(t, proto.CapAgentReply, proto.CapAgentPrompt)
	reviewer := proto.PaneInfo{ID: "r9", Name: "review · wip", State: proto.PaneRunning, ProjectID: "r1", Branch: "wip",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentWorking, Since: time.Now()}}
	mach.panes = append(mach.panes, reviewer)
	if cmd := m.sendReviewToWriter(mach.id, "r1", "wip"); cmd != nil || !strings.Contains(m.flash, "still reading") {
		t.Fatalf("while it reads: %q", m.flash)
	}
	mach.panes[1].Agent.State = proto.AgentDone
	peer.setResult(proto.MethodAgentReply, proto.AgentReply{Agent: "codex", Text: "1. a.go:12 — the error is dropped."})
	cmd := m.sendReviewToWriter(mach.id, "r1", "wip")
	if cmd == nil {
		t.Fatalf("nothing sent: %q", m.flash)
	}
	msg, ok := cmd().(promptSentMsg)
	if !ok || msg.pane != "p1" {
		t.Fatalf("sent to %#v, want the writer p1", msg)
	}
	var pp proto.AgentPromptParams
	sentParams(t, peer, proto.MethodAgentPrompt, &pp)
	if pp.ID != "p1" || !strings.Contains(pp.Text, "Codex reviewed your changes on wip") || !strings.Contains(pp.Text, "a.go:12 — the error is dropped") {
		t.Fatalf("prompt %+v", pp)
	}
	// Handed over, the reviewer is done with, and closed.
	var closed proto.PaneRef
	sentParams(t, peer, proto.MethodPaneClose, &closed)
	if closed.ID != "r9" {
		t.Fatalf("closed %q, want the reviewer r9", closed.ID)
	}
	// The queue says the review is ready, on the reviewer's row.
	var row *queueItem
	for _, it := range m.queueItems() {
		if it.paneID == "r9" {
			row = &it
		}
	}
	if row == nil || !strings.Contains(row.detail, "review ready") {
		t.Fatalf("reviewer row %+v", row)
	}
	// Failures are never sent to the reviewer, only to the writer.
	if a := m.branchAgent(mach.id, "r1", "wip"); a == nil || a.ID != "p1" {
		t.Fatalf("branch agent %+v", a)
	}
	// No reviewer, and an old server.
	if cmd := m.sendReviewToWriter(mach.id, "r1", "feat"); cmd != nil || !strings.Contains(m.flash, "no reviewer on feat") {
		t.Fatalf("no reviewer: %q", m.flash)
	}
	old, oldMach, _ := reviewModel(t, proto.CapAgentPrompt)
	oldMach.panes = append(oldMach.panes, reviewer)
	oldMach.panes[1].Agent = &proto.AgentStatus{Name: "codex", State: proto.AgentDone}
	if cmd := old.sendReviewToWriter(oldMach.id, "r1", "wip"); cmd != nil || !strings.Contains(old.flash, "predates reading") {
		t.Fatalf("old server: %q", old.flash)
	}
}

// A report that did not reach the writer leaves the reviewer, and so the
// report, where it was.
func TestReviewNotSentKeepsTheReviewer(t *testing.T) {
	m, mach, peer := reviewModel(t, proto.CapAgentReply, proto.CapAgentPrompt)
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "r9", Name: "review · wip", State: proto.PaneRunning, ProjectID: "r1", Branch: "wip",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentDone}})
	peer.setResult(proto.MethodAgentReply, proto.AgentReply{Agent: "codex", Text: "1. a.go:12"})
	peer.setError(proto.MethodAgentPrompt, "the agent is busy")
	msg, _ := m.sendReviewToWriter(mach.id, "r1", "wip")().(promptSentMsg)
	if msg.err == nil {
		t.Fatal("the failed send succeeded")
	}
	if slices.Contains(peer.methods(), proto.MethodPaneClose) {
		t.Fatal("closed the reviewer of a report that was not sent")
	}
	// Its reply unreadable: nothing sent, nothing closed.
	m2, mach2, peer2 := reviewModel(t, proto.CapAgentReply, proto.CapAgentPrompt)
	mach2.panes = append(mach2.panes, mach.panes[len(mach.panes)-1])
	peer2.setError(proto.MethodAgentReply, "no answer")
	if _, ok := m2.sendReviewToWriter(mach2.id, "r1", "wip")().(errMsg); !ok {
		t.Fatal("an unreadable report was not an error")
	}
	if ms := peer2.methods(); slices.Contains(ms, proto.MethodPaneClose) || slices.Contains(ms, proto.MethodAgentPrompt) {
		t.Fatalf("sent %v", ms)
	}
}
