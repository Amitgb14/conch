package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func reviewSetOK(t *testing.T, s *Server, c proto.ReviewComment) proto.ReviewList {
	t.Helper()
	list, perr := s.reviewSet(&client{}, c)
	if perr != nil {
		t.Fatal(perr)
	}
	return list
}

func notes(list proto.ReviewList) string {
	var out []string
	for _, c := range list.Comments {
		out = append(out, c.Note)
	}
	return strings.Join(out, ",")
}

// A comment is added, changed in place, changed by ID and removed by an
// empty note; what is wrong with one is refused.
func TestReviewSetAndList(t *testing.T) {
	s, p := afterFixture(t)
	hunk := "@@ -1,2 +1,2 @@\n-a\n+b"
	at := proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Hunk: hunk}

	at.Note = "  name it better  "
	list := reviewSetOK(t, s, at)
	if len(list.Comments) != 1 || list.Comments[0].Note != "name it better" || list.Comments[0].ID == "" || list.Comments[0].At.IsZero() {
		t.Fatalf("added: %+v", list)
	}
	id := list.Comments[0].ID

	// The same place again changes it rather than adding a second.
	at.Note = "rename it"
	if list = reviewSetOK(t, s, at); notes(list) != "rename it" || list.Comments[0].ID != id {
		t.Fatalf("again: %+v", list)
	}
	// A line and a whole file are other places.
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Line: 7, Note: "off by one"})
	list = reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "b.go", Note: "needs a test"})
	if notes(list) != "rename it,off by one,needs a test" {
		t.Fatalf("three: %q", notes(list))
	}
	// Another branch's are its own.
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/other", File: "a.go", Note: "elsewhere"})
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); len(got.Comments) != 3 {
		t.Fatalf("one branch: %+v", got)
	}
	if got := s.reviews.list(proto.ReviewRef{}); len(got.Comments) != 4 {
		t.Fatalf("all: %+v", got)
	}

	// By ID: changed, then removed.
	if list = reviewSetOK(t, s, proto.ReviewComment{ID: id, ProjectID: p.id, Branch: "api/model", Note: "rename to Model"}); list.Comments[0].Note != "rename to Model" {
		t.Fatalf("by id: %+v", list)
	}
	if list = reviewSetOK(t, s, proto.ReviewComment{ID: id, ProjectID: p.id, Branch: "api/model"}); notes(list) != "off by one,needs a test" {
		t.Fatalf("removed: %q", notes(list))
	}
	// Removing what is not there is not an error without an ID.
	at.Note = ""
	if list = reviewSetOK(t, s, at); len(list.Comments) != 2 {
		t.Fatalf("removing nothing: %+v", list)
	}

	for name, c := range map[string]proto.ReviewComment{
		"no project":      {Branch: "b", File: "a.go", Note: "x"},
		"no branch":       {ProjectID: p.id, File: "a.go", Note: "x"},
		"no file":         {ProjectID: p.id, Branch: "b", Note: "x"},
		"a bad line":      {ProjectID: p.id, Branch: "b", File: "a.go", Line: -1, Note: "x"},
		"too long":        {ProjectID: p.id, Branch: "b", File: "a.go", Note: strings.Repeat("x", reviewNoteMax+1)},
		"gone":            {ID: "c00000000", ProjectID: p.id, Branch: "api/model", Note: "x"},
		"other branch":    {ID: list.Comments[0].ID, ProjectID: p.id, Branch: "api/other", Note: "x"},
		"no such project": {ProjectID: "nope", Branch: "b", File: "a.go", Note: "x"},
	} {
		if _, perr := s.reviewSet(&client{}, c); perr == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A long hunk is kept as its start and a count of the rest, which is what
// a client quotes to find the comment on it again.
func TestReviewQuotesALongHunk(t *testing.T) {
	s, p := afterFixture(t)
	var b strings.Builder
	b.WriteString("@@ -1,50 +1,50 @@\n")
	for i := range 50 {
		b.WriteString("+line\n")
		_ = i
	}
	hunk := b.String()
	list := reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Hunk: hunk, Note: "too long"})
	got := list.Comments[0].Hunk
	if got != proto.QuoteHunk(hunk) || !strings.HasSuffix(got, "… 10 more lines") || strings.Count(got, "\n") != 1+proto.ReviewQuoteLines {
		t.Fatalf("kept:\n%s", got)
	}
	// The whole hunk again finds the same comment.
	if list = reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Hunk: hunk, Note: "shorter"}); notes(list) != "shorter" {
		t.Fatalf("again: %+v", list)
	}
}

// Comments are kept in review.json, so a restart or a reload has them; a
// file that is missing, unreadable or half written loads as none.
func TestReviewPersists(t *testing.T) {
	s, p := afterFixture(t)
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Line: 3, Note: "kept"})
	again := loadReviews(s.configDir)
	if got := again.list(proto.ReviewRef{}); notes(got) != "kept" || got.Comments[0].Line != 3 {
		t.Fatalf("loaded: %+v", got)
	}

	dir := t.TempDir()
	if got := loadReviews(dir).list(proto.ReviewRef{}); len(got.Comments) != 0 || got.Comments == nil {
		t.Fatalf("no file: %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "review.json"), []byte(`{"comments":[{"id":"c1","project_id":"r1","branch":"b","file":"a`), 0o600)
	if got := loadReviews(dir).list(proto.ReviewRef{}); len(got.Comments) != 0 {
		t.Fatalf("half written: %+v", got)
	}
	// One that makes no sense is left out, the rest kept.
	os.WriteFile(filepath.Join(dir, "review.json"), []byte(`{"comments":[
		{"id":"c1","project_id":"r1","branch":"b","file":"a.go","note":"good"},
		{"project_id":"r1","branch":"b","file":"a.go","note":"no id"},
		{"id":"c3","branch":"b","file":"a.go","note":"no project"}]}`), 0o600)
	if got := loadReviews(dir).list(proto.ReviewRef{}); notes(got) != "good" {
		t.Fatalf("mixed: %+v", got)
	}
}

// A branch that is merged or discarded takes its comments with it, and a
// project removed takes all of its own.
func TestReviewDroppedWithTheBranch(t *testing.T) {
	s, p := afterFixture(t)
	for _, br := range []string{"one", "two", "three"} {
		reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: br, File: "a.go", Note: br})
	}
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "two", File: "b.go", Note: "two more"})
	s.reviewDropped(p.id, "two")
	if got := s.reviews.list(proto.ReviewRef{}); notes(got) != "one,three" {
		t.Fatalf("one branch dropped: %q", notes(got))
	}
	if got := loadReviews(s.configDir).list(proto.ReviewRef{}); notes(got) != "one,three" {
		t.Fatalf("saved: %q", notes(got))
	}
	s.reviewDropped(p.id, "")
	if got := s.reviews.list(proto.ReviewRef{}); len(got.Comments) != 0 {
		t.Fatalf("project dropped: %+v", got)
	}

	// Through the protocol: discarding a branch.
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "x", Branch: "gone"}); perr != nil {
		t.Fatal(perr)
	}
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "gone", File: "a.go", Note: "bye"})
	for _, info := range s.list() {
		s.close(info.ID)
	}
	if _, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodBranchDiscard,
		Params: proto.Marshal(proto.BranchDiscardParams{ProjectID: p.id, Branch: "gone", DryRun: true})}); perr != nil {
		t.Fatal(perr)
	}
	if got := s.reviews.list(proto.ReviewRef{}); notes(got) != "bye" {
		t.Fatalf("a dry run dropped them: %+v", got)
	}
	if _, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodBranchDiscard,
		Params: proto.Marshal(proto.BranchDiscardParams{ProjectID: p.id, Branch: "gone", Force: true})}); perr != nil {
		t.Fatal(perr)
	}
	if got := s.reviews.list(proto.ReviewRef{}); len(got.Comments) != 0 {
		t.Fatalf("discarded: %+v", got)
	}
}

// Sending hands every comment to the branch's agent in one message and
// takes them away; the agent's screen shows what it was given.
func TestReviewSendToTheAgent(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"}); perr == nil || !strings.Contains(perr.Message, "no review comments") {
		t.Fatalf("nothing to send: %v", perr)
	}
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "model.go", Hunk: "@@ -0,0 +1 @@\n+type M struct{}", Note: "call it Model"})
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "model.go", Line: 4, Note: "unused import"})
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/other", File: "x.go", Note: "not this one"})

	// Waiting on a question: nothing is typed, and the comments stay.
	hook(s, e, "PermissionRequest")
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model", Agent: "claude"}); perr == nil || perr.Code != proto.ErrAgentBlocked {
		t.Fatalf("blocked: %v", perr)
	}
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); len(got.Comments) != 2 {
		t.Fatalf("kept after a refusal: %+v", got)
	}
	if n := len(s.list()); n != 1 {
		t.Fatalf("started another agent beside a blocked one: %d panes", n)
	}

	hook(s, e, "Stop")
	res, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Pane.ID != e.info().ID || res.Started || res.Sent != 2 {
		t.Fatalf("sent: %+v", res)
	}
	a5WaitFor(t, "the comments on the agent's screen", func() bool {
		return strings.Contains(screenText(e), "unused import")
	})
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id}); notes(got) != "not this one" {
		t.Fatalf("after sending: %+v", got)
	}
}

// With no agent on the branch, a send names one to start in its worktree,
// or is refused; a branch with no worktree has nowhere to start one.
func TestReviewSendStartsAnAgent(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	branchPane(t, s, p, "api/model")
	for _, info := range s.list() {
		s.close(info.ID)
	}
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Note: "fix it"})
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"}); perr == nil || !strings.Contains(perr.Message, "name one") {
		t.Fatalf("no agent named: %v", perr)
	}
	res, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model", Agent: "claude"})
	if perr != nil {
		t.Fatal(perr)
	}
	wt, _ := p.branchWorktree("api/model")
	if !res.Started || res.Pane.Cwd != wt.Path || res.Sent != 1 {
		t.Fatalf("started: %+v (worktree %s)", res, wt.Path)
	}
	if got := s.reviews.list(proto.ReviewRef{}); len(got.Comments) != 0 {
		t.Fatalf("after starting: %+v", got)
	}

	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "nowhere", File: "a.go", Note: "x"})
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "nowhere", Agent: "claude"}); perr == nil || !strings.Contains(perr.Message, "no worktree") {
		t.Fatalf("no worktree: %v", perr)
	}
}

// A send can name the agent, or ask for a new one beside those there: a
// branch like the base can have a dozen, and the first is rarely the one.
func TestReviewSendToTheOnePicked(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	first := branchPane(t, s, p, "api/model")
	hook(s, first, "Stop")
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Line: 3, Note: "picked note"})

	// Not a pane, not running an agent, or another project's: refused,
	// and the notes stay.
	for _, sp := range []proto.ReviewSendParams{
		{ProjectID: p.id, Branch: "api/model", Pane: "p999"},
		{ProjectID: "other", Branch: "api/model", Pane: first.info().ID},
		{ProjectID: p.id, Branch: "api/model", New: true},
	} {
		if _, perr := s.reviewSend(&client{}, sp); perr == nil {
			t.Fatalf("%+v was taken", sp)
		}
	}
	// An agent outside the project, named by its pane: refused, not a way
	// past the project the call is about.
	stray, perr := s.create(proto.PaneCreateParams{Agent: "claude", NoProject: true, Cwd: t.TempDir()})
	if perr != nil {
		t.Fatal(perr)
	}
	strayE, _ := s.get(stray.ID)
	a5WaitFor(t, "the stray agent detected", func() bool { return strayE.info().Agent != nil })
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model", Pane: stray.ID}); perr == nil || !strings.Contains(perr.Message, "not an agent of this project") {
		t.Fatalf("another project's agent: %v", perr)
	}
	s.close(stray.ID)
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); len(got.Comments) != 1 {
		t.Fatalf("kept after refusals: %+v", got)
	}

	// New: a second agent started beside the first, with the notes.
	res, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model", Agent: "claude", New: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if !res.Started || res.Pane.ID == first.info().ID {
		t.Fatalf("new: %+v", res)
	}

	// Pane: the one named, though another is older on the branch.
	second, perr := s.get(res.Pane.ID)
	if perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the new agent detected", func() bool { return second.info().Agent != nil })
	hook(s, second, "Stop")
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Line: 4, Note: "for the second"})
	res, perr = s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model", Pane: second.info().ID})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Pane.ID != second.info().ID || res.Started {
		t.Fatalf("picked: %+v", res)
	}
	a5WaitFor(t, "the note on the picked agent's screen", func() bool {
		return strings.Contains(screenText(second), "for the second")
	})
	if strings.Contains(screenText(first), "for the second") {
		t.Fatal("the first agent was given it too")
	}
}

// Two sends of the same branch at once deliver the comments once.
func TestReviewSendOnce(t *testing.T) {
	s, p := afterFixture(t)
	key := p.id + "\x00b"
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Note: "x"})
	s.reviews.sending[key] = true
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "b", Agent: "claude"}); perr == nil || !strings.Contains(perr.Message, "already") {
		t.Fatalf("second send: %v", perr)
	}
	if !s.reviews.sending[key] {
		t.Fatal("the refused send cleared the first one's mark")
	}
}

// The prompt numbers each comment and says where it is.
func TestReviewPrompt(t *testing.T) {
	got := proto.ReviewPrompt("feat", []proto.ReviewComment{
		{File: "a.go", Hunk: "@@ -1 +1 @@\n-x\n+y", Note: "why y?"},
		{File: "b.go", Line: 9, Note: "typo", By: "codex · review"},
		{File: "c.go", Note: "split this file"},
	})
	for _, want := range []string{
		"Review comments on your changes on feat:",
		"1. a.go, at @@ -1 +1 @@\n```diff\n-x\n+y\n```\nwhy y?",
		"2. b.go, line 9 (from codex · review)\ntypo",
		"3. c.go\nsplit this file",
		"say why rather than changing the code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func screenText(e *entry) string { return strings.Join(e.p.PlainLines(), "\n") }

// A comment written from an agent's pane says which agent wrote it, and
// one from outside the panes cannot claim to be an agent's.
func TestReviewSignedByTheAgent(t *testing.T) {
	s, _, work := shareFixture(t)
	p, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := agentPane(t, s, "p1", "codex", work, "stty -echo; exec cat")
	reviewer.mu.Lock()
	reviewer.project = p
	reviewer.mu.Unlock()
	caller, _ := s.scopedCaller(&client{pane: "p1"})

	list, perr := s.reviewSet(&client{pane: "p1"}, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Note: "from codex"})
	if perr != nil {
		t.Fatal(perr)
	}
	if list.Comments[0].By == "" || list.Comments[0].By != caller.label {
		t.Fatalf("by %q, want %q", list.Comments[0].By, caller.label)
	}
	list = reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "b.go", Note: "mine", By: "claude"})
	if list.Comments[1].By != "" {
		t.Fatalf("a person's comment claimed %q", list.Comments[1].By)
	}
}

// A sent note stays, marked with whom it went to, for the agent's answer:
// listed only when asked, so a client from before sees what waits as it
// always did; the agent's reply is signed, the person's puts the note back
// to be sent with its thread.
func TestReviewThread(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "Stop")
	list := reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Line: 3, Note: "check the overdraft"})
	id := list.Comments[0].ID

	res, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"})
	if perr != nil {
		t.Fatal(perr)
	}
	// The prompt names the note and says how to answer it.
	a5WaitFor(t, "the note's id on the agent's screen", func() bool {
		return strings.Contains(screenText(e), id)
	})

	ref := proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}
	if got := s.reviews.list(ref); len(got.Comments) != 0 {
		t.Fatalf("a sent note is listed as waiting: %+v", got)
	}
	sent := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model", Sent: true})
	if len(sent.Comments) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	c := sent.Comments[0]
	if c.SentPane != res.Pane.ID || c.SentTo == "" || c.SentAt == nil {
		t.Fatalf("not marked as sent: %+v", c)
	}
	// Sending again has nothing to send: it went already.
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"}); perr == nil {
		t.Fatal("a sent note was sent again")
	}

	// Refused: no id, no answer, another project, no such note.
	for _, rp := range []proto.ReviewReplyParams{
		{ProjectID: p.id, Note: "x"},
		{ProjectID: p.id, ID: id, Note: "  "},
		{ProjectID: p.id, ID: "cnothere", Note: "x"},
	} {
		if _, perr := s.reviewReply(&client{}, rp); perr == nil {
			t.Fatalf("%+v was taken", rp)
		}
	}
	if _, perr := s.reviews.reply(proto.ReviewReplyParams{ProjectID: "other", ID: id, Note: "x"}, "", time.Now()); perr == nil || !strings.Contains(perr.Message, "another project") {
		t.Fatalf("another project: %v", perr)
	}

	// The agent answers: signed, and the note stays sent.
	got, perr := s.reviews.reply(proto.ReviewReplyParams{ProjectID: p.id, ID: id, Note: "Moved the check up."}, "claude · api/model", time.Now())
	if perr != nil {
		t.Fatal(perr)
	}
	if len(got.Replies) != 1 || got.Replies[0].By != "claude · api/model" || got.SentAt == nil {
		t.Fatalf("after the agent's answer: %+v", got)
	}

	// A new note on the same line is a new thread, not the sent one changed.
	list = reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Line: 3, Note: "and the log line"})
	if len(list.Comments) != 1 || list.Comments[0].ID == id {
		t.Fatalf("the same place after sending: %+v", list)
	}

	// The person answers back through the protocol: the note waits to be
	// sent again, and the next send carries the thread.
	if _, perr := s.reviewReply(&client{}, proto.ReviewReplyParams{ProjectID: p.id, ID: id, Note: "Also cover a zero amount."}); perr != nil {
		t.Fatal(perr)
	}
	if got := s.reviews.list(ref); len(got.Comments) != 2 {
		t.Fatalf("answered back, not waiting again: %+v", got)
	}
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the thread sent again", func() bool {
		return strings.Contains(screenText(e), "Also cover a zero amount")
	})

	// Kept through a restart; resolved (an empty note), gone.
	if got := loadReviews(s.configDir).list(proto.ReviewRef{ProjectID: p.id, Sent: true}); len(got.Comments) != 2 || len(got.Comments[0].Replies) != 2 {
		t.Fatalf("saved: %+v", got)
	}
	reviewSetOK(t, s, proto.ReviewComment{ID: id, ProjectID: p.id, Branch: "api/model"})
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Sent: true}); len(got.Comments) != 1 {
		t.Fatalf("resolved: %+v", got)
	}
}

// An agent's answer through the protocol is signed by its pane's name, and
// the note stays sent; the answer is the agent's, not the person's.
func TestReviewReplySignedByTheAgent(t *testing.T) {
	s, _, work := shareFixture(t)
	p, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	agent := agentPane(t, s, "p1", "codex", work, "stty -echo; exec cat")
	agent.mu.Lock()
	agent.project = p
	agent.mu.Unlock()
	list := reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "b", File: "a.go", Line: 2, Note: "fix it"})
	id := list.Comments[0].ID
	now := time.Now()
	s.reviews.mu.Lock()
	s.reviews.comments[0].SentTo, s.reviews.comments[0].SentPane, s.reviews.comments[0].SentAt = "codex", "p1", &now
	s.reviews.mu.Unlock()

	got, perr := s.reviewReply(&client{pane: "p1"}, proto.ReviewReplyParams{ProjectID: p.id, ID: id, Note: "Fixed."})
	if perr != nil {
		t.Fatal(perr)
	}
	want := agent.info().Name
	if want == "" {
		want = "codex"
	}
	if len(got.Replies) != 1 || got.Replies[0].By != want || got.Replies[0].By == "" || got.SentAt == nil {
		t.Fatalf("signed %+v, want %q and still sent", got.Replies, want)
	}
}

// A note sent long ago goes: nobody comes back to a month-old thread, and
// the file would only grow.
func TestReviewSentNotesAge(t *testing.T) {
	old := time.Now().Add(-reviewSentKeep - time.Hour)
	recent := time.Now().Add(-time.Hour)
	if !staleSent(proto.ReviewComment{SentAt: &old}, time.Now()) || staleSent(proto.ReviewComment{SentAt: &recent}, time.Now()) ||
		staleSent(proto.ReviewComment{}, time.Now()) {
		t.Fatal("staleSent")
	}
	dir := t.TempDir()
	raw, _ := json.Marshal(reviewFile{Comments: []proto.ReviewComment{
		{ID: "c1", ProjectID: "r1", Branch: "b", File: "a.go", Note: "old", SentAt: &old},
		{ID: "c2", ProjectID: "r1", Branch: "b", File: "a.go", Note: "recent", SentAt: &recent},
		{ID: "c3", ProjectID: "r1", Branch: "b", File: "a.go", Note: "waiting"},
	}})
	if err := os.WriteFile(filepath.Join(dir, "review.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := notes(loadReviews(dir).list(proto.ReviewRef{Sent: true})); got != "recent,waiting" {
		t.Fatalf("loaded: %q", got)
	}
}
