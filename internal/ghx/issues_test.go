package ghx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIssues(t *testing.T) {
	fakeGH(t, `case "$1 $2" in
"issue list") printf '%s' '[{"number": 12, "title": "Login fails", "body": "Steps…", "url": "https://x/issues/12", "labels": [{"name": "bug"}]}, {"number": 9, "title": "Dark mode"}]' ;;
"issue view") printf '{"number": %s, "title": "One", "body": "b", "url": "u"}' "$3" ;;
*) exit 7 ;;
esac
`)
	got, err := Issues(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Number != 12 || got[0].Labels[0] != "bug" || got[1].Labels != nil {
		t.Fatalf("issues %+v", got)
	}
	one, err := IssueByNumber(context.Background(), t.TempDir(), 7)
	if err != nil || one.Number != 7 || one.Title != "One" {
		t.Fatalf("one: %+v %v", one, err)
	}
}

func TestIssuesFailures(t *testing.T) {
	fakeGH(t, "echo 'not json'\n")
	if _, err := Issues(context.Background(), t.TempDir(), 5); err == nil || !strings.Contains(err.Error(), "gh issue list") {
		t.Fatalf("bad json: %v", err)
	}
	if _, err := IssueByNumber(context.Background(), t.TempDir(), 5); err == nil || !strings.Contains(err.Error(), "gh issue view") {
		t.Fatalf("bad json: %v", err)
	}
	fakeGH(t, "echo 'GraphQL: Could not resolve to an issue with the number of 99.' >&2; exit 1\n")
	if _, err := IssueByNumber(context.Background(), t.TempDir(), 99); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("no such issue: %v", err)
	}
	t.Setenv("CONCH_GH", "/nonexistent/gh")
	if _, err := Issues(context.Background(), t.TempDir(), 5); !errors.Is(err, ErrNoGH) {
		t.Fatalf("no gh: %v", err)
	}
}
