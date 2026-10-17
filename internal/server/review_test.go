package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
