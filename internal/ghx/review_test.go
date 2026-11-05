package ghx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReview(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "got")
	fakeGH(t, `echo "$@" > `+got+`.args
cat > `+got+`.in`)
	err := Review(context.Background(), dir, 14, "request_changes", "Two things.",
		[]LineComment{{Path: "h.go", Line: 24, Body: "Don't drop the error"}})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(got + ".args")
	if !strings.Contains(string(args), "api --method POST repos/{owner}/{repo}/pulls/14/reviews --input -") {
		t.Fatalf("args %s", args)
	}
	var body struct {
		Event    string        `json:"event"`
		Body     string        `json:"body"`
		Comments []LineComment `json:"comments"`
	}
	in, _ := os.ReadFile(got + ".in")
	if json.Unmarshal(in, &body) != nil || body.Event != "REQUEST_CHANGES" || body.Body != "Two things." ||
		len(body.Comments) != 1 || body.Comments[0].Side != "RIGHT" || body.Comments[0].Line != 24 {
		t.Fatalf("payload %s", in)
	}

	// An approval needs nothing said; it sends no comments field.
	if err := Review(context.Background(), dir, 14, "approve", "", nil); err != nil {
		t.Fatal(err)
	}
	in, _ = os.ReadFile(got + ".in")
	if !strings.Contains(string(in), `"event":"APPROVE"`) || strings.Contains(string(in), "comments") {
		t.Fatalf("approval payload %s", in)
	}
}

func TestReviewRefusals(t *testing.T) {
	fakeGH(t, `echo "gh: Unprocessable Entity (HTTP 422)" >&2; exit 1`)
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		event, body string
		comments    []LineComment
		want        string
	}{
		"unknown event":        {"merge", "x", nil, "not \"merge\""},
		"comment says nothing": {"comment", "  ", nil, "needs something said"},
		"bad line":             {"comment", "", []LineComment{{Path: "a", Line: 0, Body: "x"}}, "comment 1"},
		"empty note":           {"comment", "", []LineComment{{Path: "a", Line: 3, Body: " "}}, "comment 1"},
		"gh refuses":           {"approve", "", nil, "HTTP 422"},
	} {
		err := Review(context.Background(), dir, 1, tc.event, tc.body, tc.comments)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	t.Setenv("CONCH_GH", "/nonexistent/gh")
	if err := Review(context.Background(), dir, 1, "approve", "", nil); err != ErrNoGH {
		t.Fatalf("no gh: %v", err)
	}
}
