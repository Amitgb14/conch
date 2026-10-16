package server

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/ghx"
)

func TestIssuePrompt(t *testing.T) {
	p := issuePrompt(ghx.Issue{Number: 12, Title: " Login fails on Safari ", Body: "Steps:\n1. open", URL: "https://x/issues/12"}, " keep the old API ")
	for _, want := range []string{"Work on GitHub issue #12: Login fails on Safari\nhttps://x/issues/12\n", "Steps:\n1. open", "Also: keep the old API", `"Fixes #12"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	// No body, no URL, nothing extra.
	p = issuePrompt(ghx.Issue{Number: 3, Title: "Typo"}, "")
	if strings.Contains(p, "Also:") || strings.Count(p, "\n\n") != 1 {
		t.Fatalf("bare:\n%s", p)
	}
	// A body with a whole log pasted in is cut, at valid text.
	huge := strings.Repeat("é", maxIssueBody)
	p = issuePrompt(ghx.Issue{Number: 4, Title: "Crash", Body: huge}, "")
	if len(p) > maxIssueBody+400 || !strings.Contains(p, "the rest of the issue is on GitHub") || !strings.Contains(p, "Fixes #4") {
		t.Fatalf("huge body: %d bytes", len(p))
	}
}
