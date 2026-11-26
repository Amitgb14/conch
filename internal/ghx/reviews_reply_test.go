package ghx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reply goes to the thread of the comment it answers, as JSON on stdin,
// and gives back the new comment's id.
func TestReplyToComment(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	fakeGH(t, `echo "$*" > `+filepath.Join(dir, "args")+`
cat > `+in+`
echo '{"id": 555, "body": "x"}'`)
	id, err := ReplyToComment(context.Background(), t.TempDir(), 43, 101, "done in abc")
	if err != nil || id != 555 {
		t.Fatalf("%d %v", id, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "api --method POST repos/{owner}/{repo}/pulls/43/comments/101/replies --input -") {
		t.Fatalf("asked %s", args)
	}
	body, _ := os.ReadFile(in)
	if string(body) != `{"body":"done in abc"}` {
		t.Fatalf("sent %s", body)
	}
}

func TestReplyToCommentFailures(t *testing.T) {
	if _, err := ReplyToComment(context.Background(), "", 43, 0, "x"); err == nil {
		t.Fatal("no comment to answer")
	}
	if _, err := ReplyToComment(context.Background(), "", 43, 1, "  "); err == nil {
		t.Fatal("nothing said")
	}
	fakeGH(t, "echo 'HTTP 404: Not Found' >&2; exit 1")
	if _, err := ReplyToComment(context.Background(), t.TempDir(), 43, 1, "x"); err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("gh failing: %v", err)
	}
	fakeGH(t, "echo 'not json'")
	if _, err := ReplyToComment(context.Background(), t.TempDir(), 43, 1, "x"); err == nil {
		t.Fatal("a reply that cannot be read")
	}
}

// Resolving is GraphQL's, by the thread's node id.
func TestResolveThread(t *testing.T) {
	dir := t.TempDir()
	fakeGH(t, `echo "$*" > `+filepath.Join(dir, "args")+`
echo '{"data": {"resolveReviewThread": {"thread": {"isResolved": true}}}}'`)
	if err := ResolveThread(context.Background(), t.TempDir(), "PRRT_1"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "api graphql -f id=PRRT_1") || !strings.Contains(string(args), "resolveReviewThread") {
		t.Fatalf("asked %s", args)
	}
	if err := ResolveThread(context.Background(), "", ""); err == nil {
		t.Fatal("no thread")
	}
	fakeGH(t, `echo '{"errors": [{"message": "Resource not accessible by integration"}]}'`)
	if err := ResolveThread(context.Background(), t.TempDir(), "PRRT_1"); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("refused: %v", err)
	}
	fakeGH(t, `echo '{"data": {"resolveReviewThread": {"thread": {"isResolved": false}}}}'`)
	if err := ResolveThread(context.Background(), t.TempDir(), "PRRT_1"); err == nil {
		t.Fatal("still unresolved")
	}
}

// conch's marks are hidden on GitHub and found again in what it reads.
func TestMarks(t *testing.T) {
	body := Marked(AutopilotMark, "pushed abc")
	if !HasMark(body, AutopilotMark) || HasMark(body, ReviewerMark) {
		t.Fatalf("%q", body)
	}
	if got := Unmarked(body); got != "pushed abc" {
		t.Fatalf("unmarked %q", got)
	}
	if got := Unmarked("plain"); got != "plain" {
		t.Fatalf("plain %q", got)
	}
	if got := Unmarked(Marked(ReviewerMark, "")); got != "" {
		t.Fatalf("empty %q", got)
	}
}
