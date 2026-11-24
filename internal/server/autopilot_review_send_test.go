package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A pull request's review comments, as the fake gh serves them.
type ghC struct {
	id           int64
	author, body string
}

type ghThread struct {
	id       string
	path     string
	line     int
	resolved bool
	comments []ghC
}

type ghRev struct {
	id                  int64
	author, body, state string
}

// ghAnswer is GitHub's answer to the reviews query.
func ghAnswer(threads []ghThread, reviews []ghRev) string {
	var tn, rn []map[string]any
	for _, th := range threads {
		var cs []map[string]any
		for _, c := range th.comments {
			cs = append(cs, map[string]any{"databaseId": c.id, "author": map[string]string{"login": c.author}, "body": c.body,
				"diffHunk": "@@ -1,2 +1,3 @@\n a\n+b", "url": "https://github.com/o/r/pull/5#c", "createdAt": "2026-10-01T10:00:00Z"})
		}
		tn = append(tn, map[string]any{"id": th.id, "isResolved": th.resolved, "isOutdated": false, "path": th.path, "line": th.line,
			"comments": map[string]any{"nodes": cs}})
	}
	for _, r := range reviews {
		rn = append(rn, map[string]any{"databaseId": r.id, "author": map[string]string{"login": r.author}, "body": r.body, "state": r.state})
	}
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"reviewThreads": map[string]any{"nodes": tn}, "reviews": map[string]any{"nodes": rn}}}}})
	return string(raw)
}

// reviewGH is a gh serving the reviews in answer, answering a reply with a
// new comment and resolving any thread. Every call is logged to calls, and
// each reply's body to replies.
type reviewGH struct {
	answer, calls, replies string
}

func fakeReviewGH(t *testing.T) *reviewGH {
	t.Helper()
	dir := t.TempDir()
	g := &reviewGH{answer: filepath.Join(dir, "answer.json"), calls: filepath.Join(dir, "calls"), replies: filepath.Join(dir, "replies")}
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+g.calls+`
case "$1 $2" in
"api graphql") case "$*" in
  *resolveReviewThread*) echo '{"data": {"resolveReviewThread": {"thread": {"isResolved": true}}}}' ;;
  *) cat `+g.answer+` ;;
  esac ;;
"api --method") cat >> `+g.replies+`; echo >> `+g.replies+`; echo '{"id": 900}' ;;
"pr list") echo '[]' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	return g
}

func (g *reviewGH) serve(t *testing.T, threads []ghThread, reviews []ghRev) {
	t.Helper()
	if err := os.WriteFile(g.answer, []byte(ghAnswer(threads, reviews)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sentTexts is what the autopilot typed to agents, and the notes it made.
type sentTexts struct {
	mu      sync.Mutex
	prompts []string
	notes   []proto.AutopilotNote
}

func (st *sentTexts) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.prompts)
}

func (st *sentTexts) last() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.prompts) == 0 {
		return ""
	}
	return st.prompts[len(st.prompts)-1]
}

func (st *sentTexts) noted(text string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, note := range st.notes {
		if strings.Contains(note.Text, text) {
			n++
		}
	}
	return n
}

// watchAutopilot stands in for typing to the agent, and keeps the notes.
func watchAutopilot(s *Server) *sentTexts {
	st := &sentTexts{}
	s.autopilot.prompt = func(p proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.prompts = append(st.prompts, p.Text)
		return proto.AgentPromptResult{}, nil
	}
	s.autopilot.noted = func(n proto.AutopilotNote) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.notes = append(st.notes, n)
	}
	return st
}

// reviewFixture is a project whose api/model has a free agent, pull
// request #5 open at its head, and the autopilot sending review comments.
func reviewFixture(t *testing.T) (s *Server, p *project, e *entry, g *reviewGH, st *sentTexts) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s, p = afterFixture(t)
	quietPRs(t, p)
	g = fakeReviewGH(t)
	g.serve(t, nil, nil)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e = branchPane(t, s, p, "api/model")
	hook(s, e, "Stop")
	st = watchAutopilot(s)
	s.autopilot.set(proto.AutopilotPolicy{AutoFix: 2, AutoReview: true})
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: branchHead(p, "api/model")})
	return s, p, e, g, st
}

// readReviews has the server read the pull request again, as it does once
// it moves, and the autopilot act on what it says.
func readReviews(s *Server, p *project, at time.Time) {
	s.fetchPRReviews(p, []string{"api/model"}, openPR(at))
	s.autopilotReviews()
}

// settle waits for the autopilot's sends and answers to finish.
func settle(t *testing.T, s *Server) {
	t.Helper()
	a5WaitFor(t, "the autopilot idle", func() bool {
		s.autopilot.mu.Lock()
		defer s.autopilot.mu.Unlock()
		return len(s.autopilot.reviewing) == 0 && !s.autopilot.replying
	})
}

var (
	leakThread = ghThread{id: "PRRT_1", path: "model.go", line: 12, comments: []ghC{{101, "alice", "this leaks the handle"}}}
	nameThread = ghThread{id: "PRRT_2", path: "old.go", line: 7, comments: []ghC{{300, "bob", "rename this"}}}
	needsTests = ghRev{7, "alice", "Needs tests.", "CHANGES_REQUESTED"}
)

// The first review goes to the agent as one message — each thread with its
// file and line, and the review's summary — once, and is said in the feed.
func TestAutopilotSendsTheFirstReview(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	g.serve(t, []ghThread{leakThread, nameThread}, []ghRev{needsTests})
	readReviews(s, p, t1)
	a5WaitFor(t, "the review sent", func() bool { return st.count() == 1 })
	text := st.last()
	for _, want := range []string{"model.go, line 12", "this leaks the handle", "old.go, line 7", "rename this", "Needs tests.", "pull request #5 on GitHub", "push it"} {
		if !strings.Contains(text, want) {
			t.Errorf("the message lacks %q:\n%s", want, text)
		}
	}
	settle(t, s)
	if st.noted("sent 3 review comments on #5 to") != 1 {
		t.Fatalf("notes %+v", st.notes)
	}
	// Read again, and the tick after: nothing new, nothing sent.
	readReviews(s, p, t1.Add(time.Minute))
	s.autopilotReviews()
	settle(t, s)
	if n := st.count(); n != 1 {
		t.Fatalf("sent %d times", n)
	}
	// Nothing was taken off the changes view's list: the threads are
	// still unresolved on GitHub, and still hold the merge.
	if n := len(s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments); n != 3 {
		t.Fatalf("listed %d", n)
	}
	// It names auto_pr's push when that is what pushes.
	if !strings.Contains(reviewFollowUp(5, true), "conch pushes it when you finish") {
		t.Fatal(reviewFollowUp(5, true))
	}
}

// A second review on the same pull request sends only what it adds; a
// review that only commented goes along with work, never on its own.
func TestAutopilotSendsASecondReview(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	g.serve(t, []ghThread{leakThread}, []ghRev{needsTests})
	readReviews(s, p, t1)
	a5WaitFor(t, "the first review", func() bool { return st.count() == 1 })
	settle(t, s)

	g.serve(t, []ghThread{leakThread}, []ghRev{needsTests, {8, "carol", "Just a thought.", "COMMENTED"}})
	readReviews(s, p, t1.Add(time.Hour))
	settle(t, s)
	if n := st.count(); n != 1 {
		t.Fatalf("a comment-only review went alone: %d", n)
	}

	g.serve(t, []ghThread{leakThread, nameThread}, []ghRev{needsTests, {8, "carol", "Just a thought.", "COMMENTED"}, {9, "bob", "Docs too.", "CHANGES_REQUESTED"}})
	readReviews(s, p, t1.Add(2*time.Hour))
	a5WaitFor(t, "the second review", func() bool { return st.count() == 2 })
	text := st.last()
	for _, want := range []string{"rename this", "Docs too.", "Just a thought."} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	for _, sent := range []string{"this leaks the handle", "Needs tests."} {
		if strings.Contains(text, sent) {
			t.Errorf("sent %q again:\n%s", sent, text)
		}
	}
}

// A comment edited after it went says something new, and goes again;
// so does a thread somebody adds to. The autopilot's own answer in a
// thread is not somebody adding to it.
func TestAutopilotSendsAnEditedComment(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	g.serve(t, []ghThread{leakThread, nameThread}, nil)
	readReviews(s, p, t1)
	a5WaitFor(t, "the review", func() bool { return st.count() == 1 })
	settle(t, s)

	edited := ghThread{id: "PRRT_1", path: "model.go", line: 12, comments: []ghC{{101, "alice", "this leaks the handle, and the lock"}}}
	g.serve(t, []ghThread{edited, nameThread}, nil)
	readReviews(s, p, t1.Add(time.Hour))
	a5WaitFor(t, "the edit", func() bool { return st.count() == 2 })
	if text := st.last(); !strings.Contains(text, "and the lock") || strings.Contains(text, "rename this") {
		t.Fatalf("sent:\n%s", text)
	}
	settle(t, s)

	answered := ghThread{id: "PRRT_1", path: "model.go", line: 12, comments: []ghC{{101, "alice", "this leaks the handle, and the lock"},
		{102, "amit", ghx.Marked(ghx.AutopilotMark, "claude pushed abc1234")}}}
	g.serve(t, []ghThread{answered, nameThread}, nil)
	readReviews(s, p, t1.Add(2*time.Hour))
	settle(t, s)
	if n := st.count(); n != 2 {
		t.Fatalf("its own answer was sent back: %d", n)
	}
	// The mark is not shown to the person.
	for _, c := range s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments {
		for _, r := range c.Replies {
			if strings.Contains(r.Note, "<!--") {
				t.Fatalf("mark shown: %q", r.Note)
			}
		}
	}

	replied := answered
	replied.comments = append(append([]ghC(nil), answered.comments...), ghC{103, "alice", "still leaks"})
	g.serve(t, []ghThread{replied, nameThread}, nil)
	readReviews(s, p, t1.Add(3*time.Hour))
	a5WaitFor(t, "the reply", func() bool { return st.count() == 3 })
	if text := st.last(); !strings.Contains(text, "still leaks") {
		t.Fatalf("sent:\n%s", text)
	}
}

// A thread resolved on GitHub is done with: its record goes, and it is
// never sent. Resolved and then opened again, it is news.
func TestAutopilotLeavesAResolvedThread(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	resolved := leakThread
	resolved.resolved = true
	g.serve(t, []ghThread{resolved}, nil)
	readReviews(s, p, t1)
	settle(t, s)
	if n := st.count(); n != 0 {
		t.Fatalf("a resolved thread was sent")
	}

	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, t1.Add(time.Hour))
	a5WaitFor(t, "the open thread", func() bool { return st.count() == 1 })
	settle(t, s)
	g.serve(t, []ghThread{resolved}, nil)
	readReviews(s, p, t1.Add(2*time.Hour))
	settle(t, s)
	s.autopilot.mu.Lock()
	left := len(s.autopilot.saved.ReviewSent)
	s.autopilot.mu.Unlock()
	if left != 0 {
		t.Fatalf("a resolved thread's record stayed: %d", left)
	}
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, t1.Add(3*time.Hour))
	a5WaitFor(t, "opened again", func() bool { return st.count() == 2 })
}

// What conch's own reviewer agent posted reached the agent from its report;
// it is not sent a second time from GitHub, and the mark is not shown.
func TestAutopilotSkipsConchsReviewer(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	ours := ghThread{id: "PRRT_9", path: "model.go", line: 3, comments: []ghC{{900, "amit", ghx.Marked(ghx.ReviewerMark, "the reviewer says: close it")}}}
	g.serve(t, []ghThread{ours}, []ghRev{{70, "amit", ghx.Marked(ghx.ReviewerMark, "Two findings."), "CHANGES_REQUESTED"}})
	readReviews(s, p, t1)
	settle(t, s)
	if n := st.count(); n != 0 {
		t.Fatalf("the reviewer's review was sent: %s", st.last())
	}
	for _, c := range s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments {
		if strings.Contains(c.Note, "<!--") {
			t.Fatalf("mark shown: %q", c.Note)
		}
	}
	// A person's thread beside it still goes, alone.
	g.serve(t, []ghThread{ours, leakThread}, []ghRev{{70, "amit", ghx.Marked(ghx.ReviewerMark, "Two findings."), "CHANGES_REQUESTED"}})
	readReviews(s, p, t1.Add(time.Hour))
	a5WaitFor(t, "the person's thread", func() bool { return st.count() == 1 })
	if text := st.last(); strings.Contains(text, "close it") || strings.Contains(text, "Two findings") {
		t.Fatalf("sent the reviewer's:\n%s", text)
	}
}

// A busy agent is never interrupted: the comments wait, said once, and
// go when its turn ends. With no agent at all, that is said once.
func TestAutopilotReviewWaitsForTheAgent(t *testing.T) {
	s, p, e, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	hook(s, e, "UserPromptSubmit")
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, t1)
	s.autopilotReviews()
	s.autopilotReviews()
	settle(t, s)
	if n := st.count(); n != 0 {
		t.Fatal("sent to a working agent")
	}
	if n := st.noted("wait for"); n != 1 {
		t.Fatalf("said %d times that it waits", n)
	}
	hook(s, e, "Stop")
	s.autopilotReviews()
	a5WaitFor(t, "sent once free", func() bool { return st.count() == 1 })
	settle(t, s)

	// The agent gone: nobody to send a new thread to, said once.
	s.close(e.info().ID)
	a5WaitFor(t, "the agent gone", func() bool { return s.branchAgent(p.id, "api/model") == nil })
	g.serve(t, []ghThread{leakThread, nameThread}, nil)
	readReviews(s, p, t1.Add(time.Hour))
	s.autopilotReviews()
	settle(t, s)
	if n := st.count(); n != 1 {
		t.Fatalf("sent to nobody: %d", n)
	}
	if n := st.noted("no agent is working on api/model"); n != 1 {
		t.Fatalf("said %d times there is no agent", n)
	}
}

// A message that does not reach the agent is said, and tried again at the
// next tick, never recorded as sent.
func TestAutopilotReviewSendFails(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	var mu sync.Mutex
	fail := true
	s.autopilot.prompt = func(pp proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return proto.AgentPromptResult{}, proto.Errorf(proto.ErrNotFound, "the agent went away")
		}
		st.mu.Lock()
		st.prompts = append(st.prompts, pp.Text)
		st.mu.Unlock()
		return proto.AgentPromptResult{}, nil
	}
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	settle(t, s)
	if st.noted("could not go to") != 1 {
		t.Fatalf("notes %+v", st.notes)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	s.autopilotReviews()
	a5WaitFor(t, "sent on the next try", func() bool { return st.count() == 1 })
}

// Off unless auto_review is on and the autopilot sends work back at all.
func TestAutopilotReviewOff(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	g.serve(t, []ghThread{leakThread}, []ghRev{needsTests})
	for i, pol := range []proto.AutopilotPolicy{{AutoFix: 2}, {AutoReview: true}, {}} {
		s.autopilot.set(pol)
		readReviews(s, p, time.Date(2026, 10, 1, 10+i, 0, 0, 0, time.UTC))
		settle(t, s)
		if n := st.count(); n != 0 {
			t.Fatalf("%+v sent it", pol)
		}
	}
}

// A reload between the review and the send: what was sent is kept with
// the rest of the autopilot's state, so the comments go once — the ones
// put off for a busy agent after the reload, the ones sent before it
// never again.
func TestAutopilotReviewAcrossAReload(t *testing.T) {
	s, p, e, g, st := reviewFixture(t)
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, t1)
	a5WaitFor(t, "the first thread", func() bool { return st.count() == 1 })
	settle(t, s)

	// A second thread arrives while the agent is working; then the server
	// reloads, which keeps autopilot.json and reads GitHub afresh.
	hook(s, e, "UserPromptSubmit")
	g.serve(t, []ghThread{leakThread, nameThread}, nil)
	readReviews(s, p, t1.Add(time.Hour))
	settle(t, s)
	if st.count() != 1 {
		t.Fatal("sent to a working agent")
	}
	reloadAutopilot(t, s)
	st = watchAutopilot(s)
	hook(s, e, "Stop")
	// The first tick comes before GitHub is read again: an empty book is
	// not every pull request closed.
	s.autopilotReviews()
	settle(t, s)
	s.autopilot.mu.Lock()
	kept := len(s.autopilot.saved.ReviewSent)
	s.autopilot.mu.Unlock()
	if kept != 1 {
		t.Fatalf("a tick before the read forgot what was sent: %d", kept)
	}
	readReviews(s, p, t1.Add(time.Hour))
	a5WaitFor(t, "the second thread after the reload", func() bool { return st.count() == 1 })
	if text := st.last(); !strings.Contains(text, "rename this") || strings.Contains(text, "leaks the handle") {
		t.Fatalf("after the reload:\n%s", text)
	}
	settle(t, s)
	reloadAutopilot(t, s)
	st = watchAutopilot(s)
	readReviews(s, p, t1.Add(time.Hour))
	s.autopilotReviews()
	settle(t, s)
	if n := st.count(); n != 0 {
		t.Fatalf("sent again after a second reload: %d", n)
	}
}

// reloadAutopilot is the autopilot and the review book as a reloaded
// server has them: what the files say, and no pull request read yet.
func reloadAutopilot(t *testing.T, s *Server) {
	t.Helper()
	pol := s.autopilot.policy()
	s.autopilot = loadAutopilot(s.configDir)
	if got := s.autopilot.policy(); got.AutoReview != pol.AutoReview || got.AutoFix != pol.AutoFix {
		t.Fatalf("policy after a reload: %+v", got)
	}
	s.reviews = loadReviews(s.configDir)
}

// After the agent pushes and finishes its turn, each thread it was sent
// is answered on GitHub with what changed — once — and resolved only
// where the policy says to and the push changed its file.
func TestAutopilotAnswersThreadsAfterAPush(t *testing.T) {
	s, p, e, g, st := reviewFixture(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoFix: 2, AutoReview: true, ResolveThreads: true})
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	g.serve(t, []ghThread{leakThread, nameThread}, []ghRev{needsTests})
	readReviews(s, p, t1)
	a5WaitFor(t, "the review", func() bool { return st.count() == 1 })
	settle(t, s)

	// Not pushed yet: nothing to answer.
	s.autopilotReviews()
	settle(t, s)
	if strings.Contains(readCalls(g.calls), "replies") {
		t.Fatal("answered before a push")
	}

	wt, _ := p.branchWorktree("api/model")
	os.WriteFile(filepath.Join(wt.Path, "model.go"), []byte("package api\n"), 0o644)
	apGit(t, wt.Path, "add", ".")
	apGit(t, wt.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Close the handle")
	head := branchHead(p, "api/model")

	// Pushed, but the agent is still at it: wait for its turn to end.
	hook(s, e, "UserPromptSubmit")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: head})
	s.autopilotReviews()
	settle(t, s)
	if strings.Contains(readCalls(g.calls), "replies") {
		t.Fatal("answered while the agent works")
	}

	hook(s, e, "Stop")
	s.autopilotReviews()
	settle(t, s)
	calls := readCalls(g.calls)
	if n := strings.Count(calls, "/replies"); n != 2 {
		t.Fatalf("%d answers:\n%s", n, calls)
	}
	if !strings.Contains(calls, "pulls/5/comments/101/replies") || !strings.Contains(calls, "pulls/5/comments/300/replies") {
		t.Fatalf("answered:\n%s", calls)
	}
	replies, _ := os.ReadFile(g.replies)
	for _, want := range []string{"Close the handle", "`model.go` changed in it", "`old.go` did not change in it", ghx.AutopilotMark[4:20], shortCommit(head)} {
		if !strings.Contains(string(replies), want) {
			t.Errorf("answers lack %q:\n%s", want, replies)
		}
	}
	// Resolved: the thread whose file the push changed, and no other.
	if n := strings.Count(calls, "resolveReviewThread"); n != 1 || !strings.Contains(calls, "id=PRRT_1") {
		t.Fatalf("resolved %d:\n%s", n, calls)
	}
	if st.noted("answered 2 review threads on #5 after") != 1 || st.noted("resolved 1") != 1 {
		t.Fatalf("notes %+v", st.notes)
	}

	// Answered once.
	s.autopilotReviews()
	settle(t, s)
	if n := strings.Count(readCalls(g.calls), "/replies"); n != 2 {
		t.Fatalf("answered again: %d", n)
	}
}

// Without resolve_threads, a thread is answered and left for the reviewer;
// a reply GitHub refuses is said, and not tried again for the same push.
func TestAutopilotAnswerWithoutResolving(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	a5WaitFor(t, "the review", func() bool { return st.count() == 1 })
	settle(t, s)

	wt, _ := p.branchWorktree("api/model")
	os.WriteFile(filepath.Join(wt.Path, "model.go"), []byte("package api\n"), 0o644)
	apGit(t, wt.Path, "add", ".")
	apGit(t, wt.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Close the handle")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: branchHead(p, "api/model")})

	// GitHub refuses first.
	real := os.Getenv("CONCH_GH")
	refusing := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(refusing, []byte("#!/bin/sh\necho 'HTTP 403: Resource not accessible' >&2; exit 1\n"), 0o755)
	t.Setenv("CONCH_GH", refusing)
	s.autopilotReviews()
	settle(t, s)
	s.autopilotReviews()
	settle(t, s)
	if n := st.noted("could not answer the review threads on #5"); n != 1 {
		t.Fatalf("said %d times", n)
	}

	// A new push is tried again, and answered.
	t.Setenv("CONCH_GH", real)
	os.WriteFile(filepath.Join(wt.Path, "model_test.go"), []byte("package api\n"), 0o644)
	apGit(t, wt.Path, "add", ".")
	apGit(t, wt.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Test it")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: branchHead(p, "api/model")})
	s.autopilotReviews()
	settle(t, s)
	calls := readCalls(g.calls)
	if n := strings.Count(calls, "/replies"); n != 1 {
		t.Fatalf("%d answers:\n%s", n, calls)
	}
	if strings.Contains(calls, "resolveReviewThread") {
		t.Fatal("resolved without resolve_threads")
	}
	replies, _ := os.ReadFile(g.replies)
	if !strings.Contains(string(replies), "Close the handle") || !strings.Contains(string(replies), "Test it") {
		t.Fatalf("answer:\n%s", replies)
	}
}

// Merged or closed, the pull request's records go with it; a removed
// project takes them along too.
func TestAutopilotReviewForgets(t *testing.T) {
	s, p, _, g, st := reviewFixture(t)
	g.serve(t, []ghThread{leakThread}, nil)
	readReviews(s, p, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	a5WaitFor(t, "the review", func() bool { return st.count() == 1 })
	settle(t, s)
	// Read as merged.
	p.mu.Lock()
	pr := p.prs["api/model"]
	pr.State = "MERGED"
	p.prs["api/model"] = pr
	info := p.info
	p.applyPRs(&info)
	p.mu.Unlock()
	s.autopilotPRs(info)
	s.autopilot.mu.Lock()
	left := len(s.autopilot.saved.ReviewSent)
	s.autopilot.mu.Unlock()
	if left != 0 {
		t.Fatalf("kept %d after the merge", left)
	}

	s.autopilot.mu.Lock()
	s.autopilot.saved.ReviewSent[branchKey(p.id, "api/model")+"\x00gh1"] = reviewSent{Sum: "x"}
	s.autopilot.mu.Unlock()
	s.autopilot.forget(p.id)
	if len(s.autopilot.saved.ReviewSent) != 0 {
		t.Fatal("kept after forgetting the project")
	}
}

// A review conch's reviewer agent submits carries the mark the autopilot
// leaves alone; anybody else's goes as written.
func TestMarkReview(t *testing.T) {
	rp := proto.BranchReviewParams{Body: "Two findings.", Comments: []proto.ReviewLineNew{{File: "a.go", Line: 3, Note: "close it"}}}
	if got := markReview(rp, false); got.Body != "Two findings." || got.Comments[0].Note != "close it" {
		t.Fatalf("a person's: %+v", got)
	}
	got := markReview(rp, true)
	if !ghx.HasMark(got.Body, ghx.ReviewerMark) || !ghx.HasMark(got.Comments[0].Note, ghx.ReviewerMark) || ghx.Unmarked(got.Body) != "Two findings." {
		t.Fatalf("the reviewer's: %+v", got)
	}
	if rp.Comments[0].Note != "close it" {
		t.Fatal("marked the caller's comments in place")
	}
	// An approval with nothing said stays with nothing said.
	if got := markReview(proto.BranchReviewParams{Event: "approve"}, true); got.Body != "" {
		t.Fatalf("empty body: %q", got.Body)
	}
	// The server calling itself, or a person, is no reviewer.
	s, _ := a5Server(t)
	if s.callerIsReviewer(nil) || s.callerIsReviewer(&client{}) {
		t.Fatal("taken for a reviewer")
	}
}
