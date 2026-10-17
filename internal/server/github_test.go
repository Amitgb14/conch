package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// prThreads is GitHub's answer for a pull request: alice's thread on
// model.go (with bob's reply when replied), an outdated one of bob's, and
// alice's review asking for changes.
func prThreads(replied, resolved bool) string {
	reply := ""
	if replied {
		reply = `, {"databaseId": 102, "author": {"login": "bob"}, "body": "and close it", "createdAt": "2026-10-02T10:00:00Z"}`
	}
	return `{"data": {"repository": {"pullRequest": {
 "reviewThreads": {"nodes": [
  {"isResolved": ` + map[bool]string{true: "true", false: "false"}[resolved] + `, "isOutdated": false, "path": "model.go", "line": 12,
   "comments": {"nodes": [{"databaseId": 101, "author": {"login": "alice"}, "body": "this leaks the handle",
     "diffHunk": "@@ -8,4 +8,6 @@\n f()\n+g()", "url": "https://github.com/o/r/pull/5#discussion_r101", "createdAt": "2026-10-01T10:00:00Z"}` + reply + `]}},
  {"isResolved": false, "isOutdated": true, "path": "old.go", "line": null, "originalLine": 7,
   "comments": {"nodes": [{"databaseId": 300, "author": {"login": "bob"}, "body": "rename"}]}}
 ]},
 "reviews": {"nodes": [{"databaseId": 7, "author": {"login": "alice"}, "body": "Needs tests.", "state": "CHANGES_REQUESTED"}]}
}}}}`
}

// fakePRGH is a gh that answers pr list with an open pull request #5 on
// branch, and graphql with what is in the returned file; each graphql call
// adds a line to the returned log.
func fakePRGH(t *testing.T, branch string) (answer, calls string) {
	t.Helper()
	dir := t.TempDir()
	answer, calls = filepath.Join(dir, "answer.json"), filepath.Join(dir, "calls")
	os.WriteFile(answer, []byte(prThreads(false, false)), 0o644)
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") cat <<'JSON'
[{"number":5,"title":"Model","state":"OPEN","url":"https://github.com/o/r/pull/5","headRefName":"`+branch+`","updatedAt":"2026-10-01T10:00:00Z"}]
JSON
;;
"api graphql") echo "$*" >> `+calls+`; cat `+answer+` ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	return answer, calls
}

// quietPRs keeps the server's own reading of pull requests out of the
// way, so the test is the only one reading them.
func quietPRs(t *testing.T, p *project) {
	t.Helper()
	a5WaitFor(t, "no pull requests being read", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.prNext, p.prPending = time.Now().Add(time.Hour), false
		return !p.prFetching
	})
}

func callCount(path string) int {
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), "api graphql")
}

func openPR(updated time.Time) map[string]ghx.PR {
	return map[string]ghx.PR{"api/model": {Number: 5, State: "OPEN", Branch: "api/model", Updated: updated}}
}

// A pull request's unresolved comments are listed with conch's own, as
// GitHub's, and read again only once the pull request has moved.
func TestPRReviewComments(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	answer, calls := fakePRGH(t, "api/model")
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "a.go", Note: "mine"})

	s.fetchPRReviews(p, []string{"main", "api/model"}, openPR(t1))
	got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments
	if len(got) != 4 || got[0].Note != "mine" || got[0].Source != "" {
		t.Fatalf("listed %+v", got)
	}
	gh := got[1]
	if gh.ID != "gh101" || gh.Source != proto.ReviewGitHub || gh.By != "alice" || gh.File != "model.go" || gh.Line != 12 ||
		gh.Note != "this leaks the handle" || !strings.Contains(gh.URL, "discussion_r101") || !strings.HasPrefix(gh.Hunk, "@@ -8,4") || gh.Outdated {
		t.Fatalf("thread %+v", gh)
	}
	if old := got[2]; old.File != "old.go" || old.Line != 7 || !old.Outdated || old.By != "bob" {
		t.Fatalf("outdated %+v", old)
	}
	if rv := got[3]; rv.ID != "ghr7" || rv.File != "" || rv.Note != "Needs tests." {
		t.Fatalf("review %+v", rv)
	}
	if n := callCount(calls); n != 1 {
		t.Fatalf("%d calls", n)
	}
	// Every project's list carries them too.
	if all := s.reviews.list(proto.ReviewRef{}).Comments; len(all) != 4 {
		t.Fatalf("all: %+v", all)
	}

	// GitHub's are GitHub's: not changed or removed here, and a client
	// can't pass one of its own off as GitHub's.
	for _, rc := range []proto.ReviewComment{
		{ID: "gh101", ProjectID: p.id, Branch: "api/model", Note: "edited"},
		{ID: "gh101", ProjectID: p.id, Branch: "api/model"},
	} {
		if _, perr := s.reviewSet(&client{}, rc); perr == nil || !strings.Contains(perr.Message, "on GitHub (pull request #5)") {
			t.Fatalf("%+v: %v", rc, perr)
		}
	}
	list := reviewSetOK(t, s, proto.ReviewComment{ProjectID: p.id, Branch: "api/model", File: "b.go", Note: "x", Source: proto.ReviewGitHub, By: "mallory", URL: "u"})
	if c := list.Comments[1]; c.Note != "x" || c.Source != "" || c.By != "" || c.URL != "" {
		t.Fatalf("claimed GitHub's: %+v", c)
	}

	// Not moved: not read again. Moved, or long since: read again.
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1))
	if n := callCount(calls); n != 1 {
		t.Fatalf("read an unmoved pull request again: %d", n)
	}
	s.reviews.mu.Lock()
	s.reviews.github[branchKey(p.id, "api/model")].read = time.Now().Add(-githubRefresh - time.Second)
	s.reviews.mu.Unlock()
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1))
	if n := callCount(calls); n != 2 {
		t.Fatalf("not read after %v: %d", githubRefresh, n)
	}

	// A failed read keeps what was read before.
	os.WriteFile(answer, []byte("not json"), 0o644)
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1.Add(time.Hour)))
	if n := len(s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments); n != 5 {
		t.Fatalf("after a failed read: %d", n)
	}

	// Resolved on GitHub: gone here.
	os.WriteFile(answer, []byte(prThreads(false, true)), 0o644)
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1.Add(2*time.Hour)))
	for _, c := range s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments {
		if c.ID == "gh101" {
			t.Fatal("a resolved thread is still listed")
		}
	}

	// Merged or closed, or the branch gone: the pull request's are forgotten
	// and conch's own stay.
	s.fetchPRReviews(p, []string{"api/model"}, map[string]ghx.PR{"api/model": {Number: 5, State: "MERGED", Branch: "api/model"}})
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); notes(got) != "mine,x" {
		t.Fatalf("after merging: %q", notes(got))
	}
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1.Add(3*time.Hour)))
	s.fetchPRReviews(p, []string{"main"}, openPR(t1.Add(3*time.Hour)))
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); notes(got) != "mine,x" {
		t.Fatalf("branch gone: %q", notes(got))
	}
}

// Sent to the agent, a pull request's comments are not sent again — they
// stay on GitHub, unresolved — until somebody adds to the thread, and that
// survives a restart.
func TestPRReviewCommentsSent(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	answer, _ := fakePRGH(t, "api/model")
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "Stop")
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1))

	res, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"})
	if perr != nil || res.Sent != 3 {
		t.Fatalf("sent %+v %v", res, perr)
	}
	a5WaitFor(t, "the comments on the agent's screen", func() bool {
		return strings.Contains(screenText(e), "Needs tests.")
	})
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}); len(got.Comments) != 0 {
		t.Fatalf("still offered: %+v", got)
	}
	if _, perr := s.reviewSend(&client{}, proto.ReviewSendParams{ProjectID: p.id, Branch: "api/model"}); perr == nil || !strings.Contains(perr.Message, "no review comments") {
		t.Fatalf("sent twice: %v", perr)
	}

	// A restarted server remembers what went.
	saved, _ := os.ReadFile(filepath.Join(s.configDir, "review.json"))
	var file reviewFile
	if json.Unmarshal(saved, &file) != nil || file.Handed["gh101"].Last != "101" || file.Handed["ghr7"].Branch != "api/model" {
		t.Fatalf("review.json: %s", saved)
	}
	again := loadReviews(s.configDir)
	again.setGitHub(p.id, "api/model", 5, mustReviews(t, prThreads(false, false)), t1, time.Now())
	if got := again.list(proto.ReviewRef{ProjectID: p.id}); len(got.Comments) != 0 {
		t.Fatalf("after a restart: %+v", got)
	}

	// bob answers alice's thread: it is back, reply and all.
	os.WriteFile(answer, []byte(prThreads(true, false)), 0o644)
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1.Add(time.Hour)))
	got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "api/model"}).Comments
	if len(got) != 1 || got[0].ID != "gh101" || len(got[0].Replies) != 1 || got[0].Replies[0].By != "bob" || got[0].Replies[0].Note != "and close it" {
		t.Fatalf("after a reply: %+v", got)
	}

	// Resolved: what was remembered about it goes too.
	os.WriteFile(answer, []byte(prThreads(true, true)), 0o644)
	s.fetchPRReviews(p, []string{"api/model"}, openPR(t1.Add(2*time.Hour)))
	s.reviews.mu.Lock()
	_, kept := s.reviews.handed["gh101"]
	s.reviews.mu.Unlock()
	if kept {
		t.Fatal("a resolved thread is still remembered")
	}

	// A discarded branch takes it all.
	s.reviewDropped(p.id, "api/model")
	s.reviews.mu.Lock()
	n, m := len(s.reviews.handed), len(s.reviews.github)
	s.reviews.mu.Unlock()
	if n != 0 || m != 0 {
		t.Fatalf("after dropping: %d handed, %d pull requests", n, m)
	}
}

func mustReviews(t *testing.T, raw string) *ghx.Reviews {
	t.Helper()
	rv, err := ghx.ParseReviews([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return &rv
}

// Reading the pull requests reads what was said on them.
func TestPRReviewCommentsWithThePullRequests(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	fakePRGH(t, "main")
	s.projects.fetchPRs(p)
	if got := s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "main"}); len(got.Comments) != 3 || got.Comments[0].By != "alice" {
		t.Fatalf("listed %+v", got)
	}
}

func TestGitHubHunkAndNote(t *testing.T) {
	long := "@@ -1,20 +1,20 @@"
	for i := 0; i < 20; i++ {
		long += "\n line"
	}
	for in, want := range map[string]string{
		"":                      "",
		"not a hunk":            "",
		"@@ -1 +1 @@\n-a\n+b\n": "@@ -1 +1 @@\n-a\n+b",
		long:                    "@@ -1,20 +1,20 @@\n… 8 earlier lines\n" + strings.TrimSuffix(strings.Repeat(" line\n", 12), "\n"),
	} {
		if got := githubHunk(in); got != want {
			t.Errorf("githubHunk(%q) = %q, want %q", in, got, want)
		}
	}
	thirteen := "@@ -1 +1 @@" + strings.Repeat("\n x", 13)
	if got := githubHunk(thirteen); !strings.Contains(got, "… 1 earlier line\n") {
		t.Errorf("one over: %q", got)
	}
	if got := clipNote(" a\r\nb "); got != "a\nb" {
		t.Errorf("clipNote: %q", got)
	}
	big := strings.Repeat("é", reviewNoteMax)
	if got := clipNote(big); len(got) > reviewNoteMax+40 || !strings.HasSuffix(got, "(the rest is on GitHub)") || !strings.HasPrefix(got, "é") || !utf8.ValidString(got) {
		t.Errorf("clipNote of a long one: %d bytes", len(got))
	}
}

// Pull requests are not read before git has said which branches there
// are: a read racing the project's first refresh matched them to none and
// read no review comments until the next one, minutes later.
func TestPullRequestsWaitForTheBranches(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	_, calls := fakePRGH(t, "main")
	// As just added: never refreshed, and held there so the ticker can't.
	p.mu.Lock()
	p.lastStatus, p.refreshing = time.Time{}, true
	p.prNext, p.prPending, p.prAt = time.Time{}, true, time.Time{}
	p.mu.Unlock()
	s.projects.maybeRefresh(p)
	p.mu.Lock()
	started := p.prFetching
	p.mu.Unlock()
	if started {
		t.Fatal("pull requests read before the branches were")
	}

	// Refreshed: the next tick reads them, review comments and all.
	p.mu.Lock()
	p.lastStatus = time.Now()
	p.mu.Unlock()
	s.projects.maybeRefresh(p)
	a5WaitFor(t, "the pull request's review comments", func() bool {
		return callCount(calls) > 0 && len(s.reviews.list(proto.ReviewRef{ProjectID: p.id, Branch: "main"}).Comments) == 3
	})
	p.mu.Lock()
	p.refreshing = false
	p.mu.Unlock()
}
