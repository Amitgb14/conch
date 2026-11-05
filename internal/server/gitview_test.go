package server_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestProjectCommit(t *testing.T) {
	fakeGHOn(t, "[]", "")
	c, dir, proj := setupRepo(t)
	repo := filepath.Join(dir, "api")
	if miss := c.MissingCapabilities([]string{proto.CapCommitShow, proto.CapPRReview}); len(miss) > 0 {
		t.Fatalf("capabilities missing: %v", miss)
	}
	from := revParse(t, repo, "HEAD")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "Add a\n\nThe body.")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "A does nothing")
	head := revParse(t, repo, "HEAD")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var d proto.CommitDetail
	if err := c.Call(ctx, proto.MethodProjectCommit, proto.CommitParams{ProjectID: proj.ID, Hash: "HEAD~1"}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Subject != "Add a" || d.Body != "The body." || d.Author != "t" || len(d.Files) != 1 || d.Files[0].Code != "A" || d.Files[0].Path != "a.go" {
		t.Fatalf("commit %+v", d)
	}
	var r proto.CommitDetail
	if err := c.Call(ctx, proto.MethodProjectCommit, proto.CommitParams{ProjectID: proj.ID, Hash: head, From: from}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Commits != 2 || len(r.Files) != 1 || r.Files[0].Added != 3 {
		t.Fatalf("range %+v", r)
	}
	var f proto.CommitDetail
	if err := c.Call(ctx, proto.MethodProjectCommit, proto.CommitParams{ProjectID: proj.ID, Hash: head, File: "a.go"}, &f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Diff, "+func A() {}") {
		t.Fatalf("file diff %q", f.Diff)
	}
	for name, p := range map[string]proto.CommitParams{
		"no project": {ProjectID: "nope", Hash: head},
		"an option":  {ProjectID: proj.ID, Hash: "--all"},
		"no commit":  {ProjectID: proj.ID, Hash: "deadbeef"},
	} {
		if err := c.Call(ctx, proto.MethodProjectCommit, p, nil); err == nil {
			t.Errorf("%s: answered", name)
		}
	}
}

func TestBranchReview(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "review")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") echo '[{"number":14,"title":"Health","state":"OPEN","url":"https://github.com/o/r/pull/14","headRefName":"main","statusCheckRollup":[]}]' ;;
"api --method") cat > `+got+` ;;
*) echo '[]' ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	c, _, proj := setupRepo(t)
	waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, b := range p.Branches {
			if b.Name == "main" && b.PR != nil {
				return true
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var res proto.BranchReviewResult
	err := c.Call(ctx, proto.MethodBranchReview, proto.BranchReviewParams{ProjectID: proj.ID, Branch: "main", Event: proto.ReviewEventApprove,
		Body: "Looks right.", Comments: []proto.ReviewLineNew{{File: "a.go", Line: 3, Note: "nit"}}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if res.PR != 14 || res.URL == "" {
		t.Fatalf("result %+v", res)
	}
	var sent map[string]any
	b, _ := os.ReadFile(got)
	if json.Unmarshal(b, &sent) != nil || sent["event"] != "APPROVE" || sent["body"] != "Looks right." {
		t.Fatalf("sent %s", b)
	}
	if err := c.Call(ctx, proto.MethodBranchReview, proto.BranchReviewParams{ProjectID: proj.ID, Branch: "nope", Event: "approve"}, nil); err == nil ||
		!strings.Contains(err.Error(), "no open pull request") {
		t.Fatalf("a branch without one: %v", err)
	}
}
