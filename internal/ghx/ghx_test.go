package ghx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shaped like real `gh pr list --json` output (checked against cli/cli).
const sample = `[
 {"number":3,"title":"Add login","state":"OPEN","isDraft":false,"url":"https://github.com/o/r/pull/3",
  "headRefName":"feat/login","reviewDecision":"APPROVED","updatedAt":"2026-09-11T10:00:00Z",
  "statusCheckRollup":[
   {"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS","name":"test"},
   {"__typename":"CheckRun","status":"COMPLETED","conclusion":"SKIPPED","name":"label"},
   {"__typename":"StatusContext","state":"SUCCESS","context":"ci/legacy"}]},
 {"number":2,"title":"Old login attempt","state":"CLOSED","isDraft":false,"url":"https://github.com/o/r/pull/2",
  "headRefName":"feat/login","reviewDecision":"","updatedAt":"2026-09-12T10:00:00Z","statusCheckRollup":[]},
 {"number":4,"title":"Fix flaky","state":"OPEN","isDraft":true,"url":"https://github.com/o/r/pull/4",
  "headRefName":"fix/flaky","reviewDecision":"REVIEW_REQUIRED","updatedAt":"2026-09-12T09:00:00Z",
  "statusCheckRollup":[
   {"__typename":"CheckRun","status":"IN_PROGRESS","conclusion":"","name":"test"},
   {"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE","name":"lint"}]},
 {"number":1,"title":"Docs","state":"MERGED","isDraft":false,"url":"https://github.com/o/r/pull/1",
  "headRefName":"docs","reviewDecision":"","updatedAt":"2026-09-01T09:00:00Z",
  "statusCheckRollup":[{"__typename":"StatusContext","state":"PENDING"}]}
]`

func TestParseAndByBranch(t *testing.T) {
	prs, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	by := ByBranch(prs)
	login := by["feat/login"]
	if login.Number != 3 || login.Checks != ChecksPass || login.Passed != 3 || login.Total != 3 || login.Review != "APPROVED" {
		t.Fatalf("feat/login: %+v (open PR must win over a newer closed one)", login)
	}
	if f := by["fix/flaky"]; f.Checks != ChecksFail || !f.Draft || f.Passed != 0 || f.Total != 2 {
		t.Fatalf("fix/flaky: %+v", f)
	}
	if d := by["docs"]; d.State != "MERGED" || d.Checks != ChecksPending {
		t.Fatalf("docs: %+v", d)
	}
}

func fakeGH(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", path)
}

func TestList(t *testing.T) {
	fakeGH(t, `[ "$1 $2" = "pr list" ] || exit 9
cat <<'EOF'
`+sample+`
EOF`)
	prs, err := List(context.Background(), t.TempDir())
	if err != nil || len(prs) != 4 {
		t.Fatalf("got %d prs, err %v", len(prs), err)
	}
}

func TestListErrors(t *testing.T) {
	fakeGH(t, `echo "none of the git remotes configured for this repository point to a known GitHub host" >&2
echo "more detail" >&2
exit 1`)
	_, err := List(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "known GitHub host") || strings.Contains(err.Error(), "more detail") {
		t.Fatalf("error: %v", err)
	}

	t.Setenv("CONCH_GH", filepath.Join(t.TempDir(), "missing-gh"))
	if _, err := List(context.Background(), t.TempDir()); !errors.Is(err, ErrNoGH) {
		t.Fatalf("missing gh: %v", err)
	}
}

// TestNoGHSaysWhereToGetIt: the first end-to-end run met this from p in
// the changes view, and it named the missing program without saying
// where to get it.
func TestNoGHSaysWhereToGetIt(t *testing.T) {
	if !strings.Contains(ErrNoGH.Error(), "https://cli.github.com") {
		t.Errorf("%q", ErrNoGH)
	}
}

// Each check is kept by name with its state and link — a job by its name
// and details URL, a commit status by its context and target URL — and a
// check with no name is still counted though not listed.
func TestParseRuns(t *testing.T) {
	out := `[{"number":87,"state":"OPEN","headRefName":"b","baseRefName":"master","statusCheckRollup":[
	 {"__typename":"CheckRun","name":"test (ubuntu-latest)","status":"IN_PROGRESS","conclusion":"","detailsUrl":"https://gh/run/1"},
	 {"__typename":"CheckRun","name":"test (macos-latest)","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://gh/run/2"},
	 {"__typename":"StatusContext","context":"ci/deploy-preview","state":"SUCCESS","targetUrl":"https://preview"},
	 {"__typename":"CheckRun","name":"","status":"COMPLETED","conclusion":"SUCCESS"}]}]`
	prs, err := Parse([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	pr := prs[0]
	if pr.Base != "master" || pr.Branch != "b" {
		t.Fatalf("base %q branch %q", pr.Base, pr.Branch)
	}
	want := []Check{
		{Name: "test (ubuntu-latest)", State: ChecksPending, URL: "https://gh/run/1"},
		{Name: "test (macos-latest)", State: ChecksFail, URL: "https://gh/run/2"},
		{Name: "ci/deploy-preview", State: ChecksPass, URL: "https://preview"},
	}
	if len(pr.Runs) != len(want) {
		t.Fatalf("runs %+v", pr.Runs)
	}
	for i := range want {
		if pr.Runs[i] != want[i] {
			t.Errorf("run %d: %+v, want %+v", i, pr.Runs[i], want[i])
		}
	}
	if pr.Total != 4 || pr.Passed != 2 || pr.Checks != ChecksFail {
		t.Fatalf("counts: %d/%d %s", pr.Passed, pr.Total, pr.Checks)
	}

	// A huge matrix: every job counted, the list capped.
	var b strings.Builder
	b.WriteString(`[{"number":1,"state":"OPEN","statusCheckRollup":[`)
	for i := 0; i < maxRuns+10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"__typename":"CheckRun","name":"job %d","status":"COMPLETED","conclusion":"SUCCESS"}`, i)
	}
	b.WriteString(`]}]`)
	prs, err = Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if prs[0].Total != maxRuns+10 || len(prs[0].Runs) != maxRuns {
		t.Fatalf("matrix: total %d, listed %d", prs[0].Total, len(prs[0].Runs))
	}
	// None at all: no list.
	prs, _ = Parse([]byte(`[{"number":2,"state":"OPEN","statusCheckRollup":[]}]`))
	if prs[0].Runs != nil {
		t.Fatalf("no checks: %+v", prs[0].Runs)
	}
}
