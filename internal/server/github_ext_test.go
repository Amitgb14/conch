package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// prGH is a gh with an open pull request on main that alice reviewed.
func prGH(t *testing.T) {
	t.Helper()
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") echo '[{"number":5,"title":"Model","state":"OPEN","headRefName":"main","updatedAt":"2026-10-01T10:00:00Z"}]' ;;
"api graphql") cat <<'JSON'
{"data": {"repository": {"pullRequest": {
 "reviewThreads": {"nodes": [{"isResolved": false, "path": "a.go", "line": 3,
   "comments": {"nodes": [{"databaseId": 1, "author": {"login": "alice"}, "body": "why?"}]}}]},
 "reviews": {"nodes": [{"databaseId": 2, "author": {"login": "alice"}, "body": "Needs tests.", "state": "CHANGES_REQUESTED"}]}}}}}
JSON
;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
}

// The server finds them by itself: reading the pull requests reads what
// was said on them, and the clients are told.
func TestPRReviewCommentsFetched(t *testing.T) {
	prGH(t)
	c, dir := startServer(t)
	repo := filepath.Join(dir, "web")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(15 * time.Second)
	for {
		select {
		case m, open := <-c.Events:
			if !open {
				t.Fatal("events closed")
			}
			if m.Event != proto.EventReviewChanged {
				continue
			}
			var list proto.ReviewList
			json.Unmarshal(m.Data, &list)
			if list.ProjectID == proj.ID && list.Branch == "main" && len(list.Comments) == 2 && list.Comments[0].By == "alice" {
				var listed proto.ReviewList
				if err := c.Call(ctx, proto.MethodReviewList, proto.ReviewRef{ProjectID: proj.ID, Branch: "main"}, &listed); err != nil || len(listed.Comments) != 2 {
					t.Fatalf("review.list: %+v %v", listed, err)
				}
				return
			}
		case <-timeout:
			t.Fatal("no review.changed with the pull request's comments")
		}
	}
}
