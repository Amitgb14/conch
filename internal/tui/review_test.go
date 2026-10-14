package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// writeComment opens the comment dialog on the hunk under the cursor and
// submits note.
func writeComment(t *testing.T, m *Model, cv *changesView, note string) {
	t.Helper()
	cv.key(m, a2Key("a"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("a opened no dialog (flash %q)", m.flash)
	}
	d.fields[0].in.SetValue(note)
	d.update(m, a2Key("enter"))
}

func TestReviewCommentsOnHunks(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability, proto.CapAgentPrompt)
	cv := hunkView(t, m)

	writeComment(t, m, cv, "  this should be a constant  ")
	if len(cv.comments) != 1 || cv.comments[0].note != "this should be a constant" || cv.comments[0].file != "a.go" {
		t.Fatalf("comments %+v", cv.comments)
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
	writeComment(t, m, cv, "make it a constant")
	if len(cv.comments) != 1 || cv.comments[0].note != "make it a constant" {
		t.Fatalf("edited: %+v", cv.comments)
	}
	// A second hunk, then the second file.
	cv.key(m, a2Key("n"))
	writeComment(t, m, cv, "why two?")
	cv.key(m, a2Key("esc"))
	m.focus = focusMain
	if out := a2Plain(cv.render(*m, 120, 30)); !strings.Contains(out, "✎2") || !strings.Contains(out, "S send 2 comments") {
		t.Fatalf("list:\n%s", out)
	}
	// Emptying one removes it.
	cv.key(m, a2Key("enter"))
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a4Diff("a.go", "one", "two")})
	cv.key(m, a2Key("n"))
	writeComment(t, m, cv, "   ")
	if len(cv.comments) != 1 || m.flash != "comment removed" {
		t.Fatalf("after removing: %+v (%q)", cv.comments, m.flash)
	}
}

func TestReviewCommentRefusals(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability)
	cv := hunkView(t, m)
	// A branch that isn't checked out has no agent to read them.
	cv.data.Worktree = ""
	if cmd := cv.commentHunk(m); cmd != nil || !strings.Contains(m.flash, "check it out first") {
		t.Fatalf("not checked out: %q", m.flash)
	}
	cv.data.Worktree = "/src/api-feat"
	// A diff with no hunks.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: "diff --git a/a.go b/a.go\nBinary files differ\n"})
	if cmd := cv.commentHunk(m); cmd != nil || !strings.Contains(m.flash, "no hunks") {
		t.Fatalf("binary: %q", m.flash)
	}
	// Nothing to send.
	if cmd := cv.sendReview(m); cmd != nil || !strings.Contains(m.flash, "no comments to send") {
		t.Fatalf("empty send: %q", m.flash)
	}
}

func TestSendReviewToTheAgent(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability, hunksCapability, proto.CapAgentPrompt)
	cv := hunkView(t, m)
	writeComment(t, m, cv, "rename this")
	cv.key(m, a2Key("n"))
	writeComment(t, m, cv, "add a test")
	_, cmd := cv.key(m, a2Key("S"))
	if cmd == nil {
		t.Fatalf("S sent nothing: %q", m.flash)
	}
	if len(cv.comments) != 0 {
		t.Fatal("comments kept after sending")
	}
	if msg, ok := cmd().(promptSentMsg); !ok || msg.pane != "p1" || msg.err != nil {
		t.Fatalf("sent %#v", msg)
	}
	var pp proto.AgentPromptParams
	peer.mu.Lock()
	for _, msg := range peer.msgs {
		if msg.Method == proto.MethodAgentPrompt {
			json.Unmarshal(msg.Params, &pp)
		}
	}
	peer.mu.Unlock()
	for _, want := range []string{"Review comments on your changes on feat", "1. a.go, at @@ -1,3 +1,3 @@", "+one", "rename this", "2. a.go, at @@ -20,3 +20,3 @@", "add a test", "say why"} {
		if !strings.Contains(pp.Text, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, pp.Text)
		}
	}
	// A refused send keeps nothing back from the person: the comments went
	// with the message, and the error says why it was refused.
	peer.setError(proto.MethodAgentPrompt, "claude is waiting on a question")
	writeComment(t, m, cv, "again")
	_, cmd = cv.key(m, a2Key("S"))
	m.receivePromptSent(cmd().(promptSentMsg))
	if !strings.Contains(m.flash, "waiting on a question") {
		t.Fatalf("refused: %q", m.flash)
	}
}

func TestReviewPromptQuotesLongHunksShort(t *testing.T) {
	var body []string
	for range 100 {
		body = append(body, "+line")
	}
	text := reviewPrompt("feat", []reviewComment{{file: "big.go", hunk: "@@ -1 +1,100 @@\n" + strings.Join(body, "\n") + "\n", note: "too long"}})
	if strings.Count(text, "+line") != maxHunkQuote || !strings.Contains(text, "… 60 more lines") {
		t.Fatalf("quoted %d lines:\n%s", strings.Count(text, "+line"), text)
	}
}
