package proto

import (
	"strings"
	"testing"
)

func TestQuoteHunk(t *testing.T) {
	hunk := func(n int) string {
		lines := []string{"@@ -1 +1 @@"}
		for range n {
			lines = append(lines, "+x")
		}
		return strings.Join(lines, "\n") + "\n"
	}
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"a header alone", "@@ -1 +1 @@\n", "@@ -1 +1 @@"},
		{"exactly the limit", hunk(ReviewQuoteLines), strings.TrimRight(hunk(ReviewQuoteLines), "\n")},
		{"one over", hunk(ReviewQuoteLines + 1), strings.TrimRight(hunk(ReviewQuoteLines), "\n") + "\n… 1 more line"},
	} {
		if got := QuoteHunk(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Quoting what was quoted changes nothing, so a client and the server
	// agree whichever of them quoted first.
	for _, n := range []int{ReviewQuoteLines + 1, 100} {
		if q := QuoteHunk(hunk(n)); QuoteHunk(q) != q || !strings.HasSuffix(q, " more line"+map[bool]string{true: "", false: "s"}[n == ReviewQuoteLines+1]) {
			t.Errorf("%d lines quoted twice: %q", n, QuoteHunk(q))
		}
	}
}

func TestReviewWhereAndAuthor(t *testing.T) {
	for _, c := range []struct {
		rc          ReviewComment
		where, from string
	}{
		{ReviewComment{File: "a.go"}, "a.go", ""},
		{ReviewComment{File: "a.go", Line: 7, By: "codex · review"}, "a.go:7", "codex · review"},
		{ReviewComment{File: "a.go", Hunk: "@@ -1 +1 @@\n-x\n+y"}, "a.go @@ -1 +1 @@", ""},
		// GitHub's have both; the line says more.
		{ReviewComment{File: "a.go", Line: 3, Hunk: "@@ -1 +1 @@", By: "alice", Source: ReviewGitHub}, "a.go:3", "alice on GitHub"},
		{ReviewComment{Note: "Needs tests.", Source: ReviewGitHub}, "the pull request", "GitHub"},
	} {
		if got := ReviewWhere(c.rc); got != c.where {
			t.Errorf("where %+v = %q, want %q", c.rc, got, c.where)
		}
		if got := ReviewAuthor(c.rc); got != c.from {
			t.Errorf("author %+v = %q, want %q", c.rc, got, c.from)
		}
	}
}

func TestReviewPromptFromGitHub(t *testing.T) {
	got := ReviewPrompt("feat", []ReviewComment{
		{File: "model.go", Line: 12, Hunk: "@@ -8,2 +8,3 @@\n f()\n+g()", Note: "this leaks the handle", By: "alice", Source: ReviewGitHub,
			Replies: []ReviewReply{{By: "bob", Note: "and close it\nin a defer"}, {Note: "?"}}},
		{File: "old.go", Line: 7, Note: "rename", By: "bob", Source: ReviewGitHub, Outdated: true},
		{Note: "Needs tests.", By: "alice", Source: ReviewGitHub},
		{File: "c.go", Hunk: "@@ -1 +1 @@", Note: "header only"},
	})
	for _, want := range []string{
		"1. model.go, line 12 (from alice on GitHub)\n```diff\n f()\n+g()\n```\nthis leaks the handle\n",
		"  Reply from bob: and close it\n  in a defer\n",
		"  Reply from someone: ?\n",
		"2. old.go, line 7 (from bob on GitHub; outdated: those lines have changed since)\nrename\n",
		"3. On the pull request as a whole (from alice on GitHub)\nNeeds tests.\n",
		"4. c.go, at @@ -1 +1 @@\n```diff\n\n```\nheader only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
