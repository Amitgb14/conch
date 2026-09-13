package ghx

import (
	"context"
	"errors"
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
