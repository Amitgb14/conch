package ghx

import (
	"context"
	"strings"
	"testing"
)

const reviewsJSON = `{"data": {"repository": {"pullRequest": {
 "reviewThreads": {"nodes": [
  {"isResolved": false, "isOutdated": false, "path": "api/model.go", "line": 12, "originalLine": 10,
   "comments": {"nodes": [
    {"databaseId": 101, "author": {"login": "alice"}, "body": "this leaks the handle", "diffHunk": "@@ -8,4 +8,6 @@\n f()\n+g()", "url": "https://x/c101", "createdAt": "2026-10-01T10:00:00Z"},
    {"databaseId": 102, "author": null, "body": "agreed", "url": "https://x/c102", "createdAt": "2026-10-01T11:00:00Z"}]}},
  {"isResolved": true, "isOutdated": false, "path": "done.go", "line": 1,
   "comments": {"nodes": [{"databaseId": 200, "author": {"login": "bob"}, "body": "fixed"}]}},
  {"isResolved": false, "isOutdated": true, "path": "old.go", "line": null, "originalLine": 7,
   "comments": {"nodes": [{"databaseId": 300, "author": {"login": "bob"}, "body": "rename"}]}},
  {"isResolved": false, "isOutdated": false, "path": "empty.go", "line": 3, "comments": {"nodes": []}}
 ]},
 "reviews": {"nodes": [
  {"databaseId": 7, "author": {"login": "alice"}, "body": "Needs tests.", "url": "https://x/r7", "state": "CHANGES_REQUESTED", "submittedAt": "2026-10-01T12:00:00Z"},
  {"databaseId": 8, "author": {"login": "bob"}, "body": "LGTM", "state": "APPROVED"},
  {"databaseId": 9, "author": {"login": "bob"}, "body": "", "state": "COMMENTED"},
  {"databaseId": 10, "author": {"login": "bob"}, "body": "draft", "state": "PENDING"},
  {"databaseId": 11, "author": {"login": "carol"}, "body": "one question", "state": "COMMENTED"}
 ]}
}}}}`

func TestPRReviews(t *testing.T) {
	fakeGH(t, `[ "$1 $2" = "api graphql" ] || exit 9
case "$*" in *"owner={owner}"*"name={repo}"*"number=43"*"reviewThreads"*) ;; *) exit 8 ;; esac
cat <<'EOF'
`+reviewsJSON+`
EOF`)
	got, err := PRReviews(context.Background(), t.TempDir(), 43)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 2 {
		t.Fatalf("threads %+v", got.Threads)
	}
	th := got.Threads[0]
	if th.Path != "api/model.go" || th.Line != 12 || th.Outdated || !strings.HasPrefix(th.Hunk, "@@ -8,4") || len(th.Comments) != 2 {
		t.Fatalf("first %+v", th)
	}
	if c := th.Comments[0]; c.ID != 101 || c.Author != "alice" || c.Body != "this leaks the handle" || c.URL != "https://x/c101" || c.At.IsZero() {
		t.Fatalf("comment %+v", c)
	}
	// A deleted account is GitHub's ghost.
	if th.Comments[1].Author != "ghost" {
		t.Fatalf("reply by %q", th.Comments[1].Author)
	}
	// Outdated: where it was written, and said.
	if old := got.Threads[1]; old.Path != "old.go" || old.Line != 7 || !old.Outdated {
		t.Fatalf("outdated %+v", old)
	}
	if len(got.Reviews) != 2 || got.Reviews[0].ID != 7 || got.Reviews[0].State != "CHANGES_REQUESTED" || got.Reviews[1].Author != "carol" {
		t.Fatalf("reviews %+v", got.Reviews)
	}
}

func TestPRReviewsFailures(t *testing.T) {
	for name, tc := range map[string]struct{ out, want string }{
		"not json":     {`oops`, "parse gh api graphql"},
		"truncated":    {`{"data": {"repository":`, "parse gh api graphql"},
		"graphql":      {`{"errors": [{"message": "Could not resolve to a PullRequest"}]}`, "Could not resolve"},
		"no such pull": {`{"data": {"repository": {"pullRequest": null}}}`, "no such pull request"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeGH(t, "cat <<'EOF'\n"+tc.out+"\nEOF")
			if _, err := PRReviews(context.Background(), t.TempDir(), 1); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
		})
	}
	// gh failing says what gh said.
	fakeGH(t, "echo 'HTTP 401: Bad credentials' >&2; exit 1")
	if _, err := PRReviews(context.Background(), t.TempDir(), 1); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("gh failing: %v", err)
	}
	// Nothing to say is not a failure.
	fakeGH(t, `echo '{"data": {"repository": {"pullRequest": {"reviewThreads": {"nodes": []}, "reviews": {"nodes": []}}}}}'`)
	if got, err := PRReviews(context.Background(), t.TempDir(), 1); err != nil || len(got.Threads)+len(got.Reviews) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
}
