package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestTaskFromAnIssue(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") echo '[]' ;;
"issue list") echo '[{"number": 12, "title": "Login fails", "url": "https://x/12", "labels": [{"name": "bug"}]}]' ;;
"issue view") [ "$3" = 12 ] || { echo "GraphQL: Could not resolve to an issue with the number of $3." >&2; exit 1; }
  echo '{"number": 12, "title": "Login fails on Safari", "body": "Clicking login does nothing.", "url": "https://x/12"}' ;;
*) exit 7 ;;
esac
`), 0o755)
	c, _, worktree := taskFixture(t, "exec sleep 30")
	t.Setenv("CONCH_GH", gh) // after the fixture, which sets one of its own
	ctx := a5Ctx(t)
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil || len(list.Projects) != 1 {
		t.Fatalf("projects: %v %v", list, err)
	}
	proj := list.Projects[0]

	var issues proto.IssueList
	if err := c.Call(ctx, proto.MethodProjectIssues, proto.IssuesParams{ProjectID: proj.ID}, &issues); err != nil {
		t.Fatal(err)
	}
	if len(issues.Issues) != 1 || issues.Issues[0].Number != 12 || issues.Issues[0].Labels[0] != "bug" {
		t.Fatalf("issues %+v", issues)
	}

	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Issue: 12, Cols: 60, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.Branch, "issue-12-login-fails-safari") {
		t.Fatalf("branch %q", info.Branch)
	}
	if _, err := os.Stat(worktree(strings.ReplaceAll(info.Branch, "/", "-"))); err != nil {
		t.Fatalf("no worktree: %v", err)
	}

	// An issue that isn't there says so in gh's words, and makes nothing.
	err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Issue: 99}, nil)
	if err == nil || !strings.Contains(err.Error(), "issue #99") || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("missing issue: %v", err)
	}
	// Neither a prompt nor an issue.
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID}, nil); err == nil || !strings.Contains(err.Error(), "needs a prompt") {
		t.Fatalf("empty task: %v", err)
	}
}
