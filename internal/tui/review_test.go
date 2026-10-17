package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func commentModel(t *testing.T) (*Model, *a1Peer) {
	t.Helper()
	return harvestModel(t, harvestCapability, hunksCapability, proto.CapAgentPrompt, proto.CapReviewComments)
}

// writeComment writes note on the hunk under the cursor, the server
// answering with reply, and says what was sent to it.
func writeComment(t *testing.T, m *Model, peer *a1Peer, cv *changesView, note string, reply ...proto.ReviewComment) proto.ReviewComment {
	t.Helper()
	peer.setResult(proto.MethodReviewSet, proto.ReviewList{ProjectID: "r1", Branch: "feat", Comments: append([]proto.ReviewComment{}, reply...)})
	before := len(peer.snapshot())
	cv.key(m, a2Key("a"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("a opened no dialog (flash %q)", m.flash)
	}
	d.fields[0].in.SetValue(note)
	_, cmd := d.update(m, a2Key("enter"))
	for _, msg := range a2Run(cmd) {
		if rm, ok := msg.(reviewsMsg); ok {
			m.receiveReviews(rm)
		} else if msg != nil {
			t.Fatalf("writing a comment gave %#v", msg)
		}
	}
	msgs := peer.snapshot()[before:]
	if len(msgs) != 1 || msgs[0].Method != proto.MethodReviewSet {
		t.Fatalf("sent %v", msgs)
	}
	var sent proto.ReviewComment
	json.Unmarshal(msgs[0].Params, &sent)
	return sent
}

func onHunk(file, hunk, note string) proto.ReviewComment {
	return proto.ReviewComment{ID: "c" + note, ProjectID: "r1", Branch: "feat", File: file, Hunk: proto.QuoteHunk(hunk), Note: note}
}

func TestReviewCommentsOnHunks(t *testing.T) {
	m, peer := commentModel(t)
	cv := hunkView(t, m)
	_, hunks, _ := cv.hunksOf()

	first := onHunk("a.go", hunks[0], "this should be a constant")
	sent := writeComment(t, m, peer, cv, "  this should be a constant  ", first)
	if sent.ProjectID != "r1" || sent.Branch != "feat" || sent.File != "a.go" || sent.Hunk != hunks[0] || sent.Note != "this should be a constant" {
		t.Fatalf("sent %+v", sent)
	}
	if !strings.Contains(m.flash, "1 comment on feat") {
		t.Fatalf("flash %q", m.flash)
	}
	// The hunk shows it, in either layout, and the title counts them.
	for _, split := range []bool{false, true} {
		cv.unsplit = !split
		out := a2Plain(cv.render(*m, 160, 30))
		if !strings.Contains(out, "▸✎") || !strings.Contains(out, "✎ 1 comment · S send") {
			t.Fatalf("split=%v:\n%s", split, out)
		}
	}
	// a again edits it; the dialog starts from what was written.
	cv.key(m, a2Key("a"))
	if d := m.overlay.(*dialog); d.fields[0].in.Value() != "this should be a constant" {
		t.Fatalf("editing starts from %q", d.fields[0].in.Value())
	}
	m.overlay = nil
	// Emptying it asks the server to remove it.
	if sent = writeComment(t, m, peer, cv, "   "); sent.Note != "" || m.flash != "comment removed" {
		t.Fatalf("removing: %+v (%q)", sent, m.flash)
	}
	if out := a2Plain(cv.render(*m, 160, 30)); strings.Contains(out, "✎") {
		t.Fatalf("still marked:\n%s", out)
	}

	// Comments written elsewhere — another TUI, the command line, an agent —
	// arrive as an event and show the same way.
	two := proto.ReviewList{ProjectID: "r1", Branch: "feat", Comments: []proto.ReviewComment{
		onHunk("a.go", hunks[0], "one"), onHunk("a.go", hunks[1], "two"),
	}}
	m.handleEvent(m.machines[0], proto.Message{Event: proto.EventReviewChanged, Data: proto.Marshal(two)})
	// Another branch's do not count here.
	m.handleEvent(m.machines[0], proto.Message{Event: proto.EventReviewChanged, Data: proto.Marshal(proto.ReviewList{
		ProjectID: "r1", Branch: "other", Comments: []proto.ReviewComment{{ProjectID: "r1", Branch: "other", File: "a.go", Note: "x"}}})})
	cv.key(m, a2Key("esc"))
	m.focus = focusMain
	if out := a2Plain(cv.render(*m, 120, 30)); !strings.Contains(out, "✎2") || !strings.Contains(out, "S send 2 comments") {
		t.Fatalf("list:\n%s", out)
	}
	// Sent from elsewhere: gone here too.
	m.handleEvent(m.machines[0], proto.Message{Event: proto.EventReviewChanged, Data: proto.Marshal(proto.ReviewList{ProjectID: "r1", Branch: "feat"})})
	if out := a2Plain(cv.render(*m, 120, 30)); strings.Contains(out, "✎") || len(m.machines[0].reviews) != 1 {
		t.Fatalf("after sending elsewhere (%d left):\n%s", len(m.machines[0].reviews), out)
	}
}

// Connecting loads every branch's comments.
func TestReviewCommentsLoaded(t *testing.T) {
	m, peer := commentModel(t)
	mach := m.machines[0]
	peer.setResult(proto.MethodReviewList, proto.ReviewList{Comments: []proto.ReviewComment{
		{ProjectID: "r1", Branch: "feat", File: "a.go", Note: "x"}, {ProjectID: "r1", Branch: "wip", File: "b.go", Note: "y"},
	}})
	m.Update(mach.loadReviews()())
	if got := mach.reviewsOn("r1", "feat"); len(got) != 1 || got[0].Note != "x" {
		t.Fatalf("loaded %+v", mach.reviews)
	}

	old, _ := harvestModel(t, harvestCapability, hunksCapability)
	if cmd := old.machines[0].loadReviews(); cmd != nil {
		t.Fatal("asked an older server for comments")
	}
}

func TestReviewCommentRefusals(t *testing.T) {
	m, _ := commentModel(t)
	cv := hunkView(t, m)
	// A branch that isn't checked out has no agent to read them.
	cv.data.Worktree = ""
	if cmd := cv.commentHunk(m); cmd != nil || !strings.Contains(m.flash, "check it out first") {
		t.Fatalf("not checked out: %q", m.flash)
	}
	cv.data.Worktree = "/src/api-feat"
	// Nothing to send.
	if cmd := cv.sendReview(m); cmd != nil || !strings.Contains(m.flash, "no comments to send") {
		t.Fatalf("empty send: %q", m.flash)
	}
	// A diff with no hunks.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: "diff --git a/a.go b/a.go\nBinary files differ\n"})
	if cmd := cv.commentHunk(m); cmd != nil || !strings.Contains(m.flash, "no hunks") {
		t.Fatalf("binary: %q", m.flash)
	}

	// A server from before review comments: said, not kept here instead.
	old, _ := harvestModel(t, harvestCapability, hunksCapability, proto.CapAgentPrompt)
	ocv := hunkView(t, old)
	if cmd := ocv.commentHunk(old); cmd != nil || old.overlay != nil || !strings.Contains(old.flash, "predates review comments") {
		t.Fatalf("old server, a: %q", old.flash)
	}
	old.machines[0].reviews = []proto.ReviewComment{{ProjectID: "r1", Branch: "feat", File: "a.go", Note: "x"}}
	if cmd := ocv.sendReview(old); cmd != nil || !strings.Contains(old.flash, "predates review comments") {
		t.Fatalf("old server, S: %q", old.flash)
	}
}

func TestSendReviewToTheAgent(t *testing.T) {
	m, peer := commentModel(t)
	cv := hunkView(t, m)
	_, hunks, _ := cv.hunksOf()
	m.machines[0].reviews = []proto.ReviewComment{onHunk("a.go", hunks[0], "rename this"), onHunk("a.go", hunks[1], "add a test")}

	peer.setResult(proto.MethodReviewSend, proto.ReviewSendResult{Pane: proto.PaneInfo{ID: "p1", Agent: &proto.AgentStatus{Name: "claude"}}, Sent: 2, Turn: 3})
	_, cmd := cv.key(m, a2Key("S"))
	if cmd == nil {
		t.Fatalf("S sent nothing: %q", m.flash)
	}
	msg, ok := cmd().(promptSentMsg)
	if !ok || msg.pane != "p1" || msg.err != nil || msg.res.Turn != 3 || msg.res.Agent != "claude" {
		t.Fatalf("sent %#v", msg)
	}
	sp := lastParams[proto.ReviewSendParams](t, peer, proto.MethodReviewSend)
	if sp.ProjectID != "r1" || sp.Branch != "feat" || sp.Agent != m.defaultAgent() || sp.Cols <= 0 || sp.Rows <= 0 {
		t.Fatalf("params %+v", sp)
	}
	// You are told when the work it starts ends, as for any message.
	m.receivePromptSent(msg)
	if !strings.Contains(m.flash, "you'll be told") {
		t.Fatalf("flash %q", m.flash)
	}

	// No agent there: the server started one.
	peer.setResult(proto.MethodReviewSend, proto.ReviewSendResult{Pane: proto.PaneInfo{ID: "p9"}, Started: true, Sent: 2})
	_, cmd = cv.key(m, a2Key("S"))
	if f, ok := cmd().(flashMsg); !ok || !strings.Contains(string(f), "started") || !strings.Contains(string(f), "2 review comments") {
		t.Fatalf("started: %#v", f)
	}

	// Refused: the server's words, and the comments stay where they were.
	peer.setError(proto.MethodReviewSend, "claude in p1 is waiting for an answer; nothing was typed")
	_, cmd = cv.key(m, a2Key("S"))
	m.receivePromptSent(cmd().(promptSentMsg))
	if !strings.Contains(m.flash, "waiting for an answer") || len(m.machines[0].reviews) != 2 {
		t.Fatalf("refused: %q", m.flash)
	}
}

func fromGitHub(id, file string, line int, by, note string) proto.ReviewComment {
	return proto.ReviewComment{ID: id, ProjectID: "r1", Branch: "feat", File: file, Line: line, Note: note, By: by,
		Source: proto.ReviewGitHub, URL: "https://github.com/o/r/pull/5#" + id}
}

// What reviewers said on the pull request is marked on the hunk it is
// about and read under the diff's title, with who said it.
func TestReviewCommentsFromGitHub(t *testing.T) {
	m, _ := commentModel(t)
	cv := hunkView(t, m)
	thread := fromGitHub("gh1", "a.go", 21, "alice", "this leaks\n  the handle")
	thread.Replies = []proto.ReviewReply{{By: "bob", Note: "agreed"}}
	old := fromGitHub("gh2", "a.go", 7, "bob", "rename")
	old.Outdated = true
	m.machines[0].reviews = []proto.ReviewComment{
		thread, old,
		fromGitHub("gh3", "b.go", 2, "carol", "elsewhere"),
		fromGitHub("ghr4", "", 0, "alice", "Needs tests."),
		fromGitHub("gh5", "a.go", 0, "dan", "split this file"),
	}
	cv.syncComments(*m)
	// Line 21 is in the second hunk (+20,3), and only there.
	if got := cv.hunkComments(1); len(got) != 1 || got[0].ID != "gh1" {
		t.Fatalf("second hunk: %+v", got)
	}
	if got := cv.hunkComments(0); len(got) != 0 {
		t.Fatalf("first hunk: %+v", got)
	}
	if cv.hunkMark(1) == " " || cv.hunkMark(0) != " " {
		t.Fatal("marks")
	}
	// GitHub's are not this conch's to edit: a on that hunk writes anew.
	if _, ok := cv.commentAt(1); ok {
		t.Fatal("a GitHub comment taken for ours")
	}

	cv.hunkSel = 1
	out := a2Plain(cv.render(*m, 160, 30))
	for _, want := range []string{
		"✎ 5 comments (5 from GitHub) · S send",
		"✎ alice on GitHub · line 21 · 1 reply: this leaks the handle",
		"✎ bob on GitHub · a.go:7 (outdated): rename",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "elsewhere") {
		t.Fatalf("another file's comment shown:\n%s", out)
	}
	// Three lines at most; the rest are counted.
	if !strings.Contains(out, "… 2 more · conch review lists them all") {
		t.Fatalf("no count of the rest:\n%s", out)
	}
	cv.hunkSel = 0
	if out := a2Plain(cv.render(*m, 160, 30)); strings.Contains(out, "line 21") || !strings.Contains(out, "a.go:7 (outdated)") || !strings.Contains(out, "a.go: split this file") {
		t.Fatalf("on the first hunk:\n%s", out)
	}

	// Narrow and short: nothing runs past the edge, and a short view keeps
	// its lines for the diff.
	for _, w := range []int{1, 5, 20, 30, 200} {
		for i, l := range cv.commentLines(w, 3) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %d is %d wide: %q", w, i, ansi.StringWidth(l), l)
			}
		}
		cv.render(*m, w, w)
	}
	if got := cv.commentLines(80, 0); got != nil {
		t.Fatalf("no room: %q", got)
	}
	if out := a2Plain(cv.render(*m, 160, 7)); strings.Contains(out, "rename") {
		t.Fatalf("comments in a short view:\n%s", out)
	}
}

func TestNewRangeOf(t *testing.T) {
	for header, want := range map[string][2]int{
		"@@ -3,4 +5,6 @@ func x()": {5, 6},
		"@@ -1 +1 @@":              {1, 1},
		"@@ -1,2 +0,0 @@":          {0, 0}, // all deleted: no new lines to be on
		"@@ -0,0 +1,3 @@":          {1, 3},
		"@@ -1 +x @@":              {0, 0},
		"not a header":             {0, 0},
		"@@ -1 +4,y @@":            {4, 1},
	} {
		if s, n := newRangeOf(header); s != want[0] || n != want[1] {
			t.Errorf("%q: %d,%d want %v", header, s, n, want)
		}
	}
}
