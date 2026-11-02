package server_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// project.log reads the whole repository from the main checkout: a commit
// made in a branch's worktree is there, with its branch named.
func TestProjectLog(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-q", "-m", "on feat")
	git(t, repo, "tag", "v1", "main")

	var res proto.LogResult
	if err := call(t, c, proto.MethodProjectLog, proto.LogParams{ProjectID: proj.ID}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 2 || res.More {
		t.Fatalf("log: %+v", res)
	}
	top, root := res.Commits[0], res.Commits[1]
	if top.Subject != "on feat" || !slices.Contains(top.Refs, "feat") || len(top.Parents) != 1 || top.Parents[0] != root.Hash {
		t.Errorf("newest: %+v", top)
	}
	if root.Subject != "init" || !slices.Contains(root.Refs, "tag: v1") || root.Time.IsZero() || root.Author != "t" {
		t.Errorf("root: %+v", root)
	}

	// A limit keeps the newest and says there is more.
	res = proto.LogResult{}
	if err := call(t, c, proto.MethodProjectLog, proto.LogParams{ProjectID: proj.ID, Limit: 1}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 || !res.More || res.Commits[0].Subject != "on feat" {
		t.Errorf("limit 1: %+v", res)
	}
}

// An unknown project is an error, as everywhere else; so is one that is
// not a git repository.
func TestProjectLogRefusals(t *testing.T) {
	c, dir := startServer(t)
	if err := call(t, c, proto.MethodProjectLog, proto.LogParams{ProjectID: "nope"}, nil); err == nil {
		t.Error("unknown project: no error")
	}
	plain := filepath.Join(dir, "plain")
	os.MkdirAll(plain, 0o755)
	var proj proto.ProjectInfo
	if err := call(t, c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: plain}, &proj); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.Path == plain })
	if err := call(t, c, proto.MethodProjectLog, proto.LogParams{ProjectID: proj.ID}, nil); err == nil {
		t.Error("not a repository: no error")
	}
}
