package server_test

import (
	"os"
	"os/exec"
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
	// The repository's format comes with it: a SHA-1 repository with the
	// files backend, as git 2 makes them.
	if res.Format == nil || res.Format.Objects != "sha1" || res.Format.Refs != "files" {
		t.Errorf("format: %+v", res.Format)
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

// A rebase that stops on a conflict detaches the worktree's HEAD, so git
// names no branch there; the worktree says which branch the rebase is
// rewriting, or a client could not tell whose conflict it is.
func TestWorktreeRebasingBranch(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	git(t, wt, "config", "user.name", "t")
	git(t, wt, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "commit", "-q", "-am", "on feat")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "on main")
	if out, err := exec.Command("git", "-C", wt, "rebase", "main").CombinedOutput(); err == nil {
		t.Fatalf("rebase did not stop: %s", out)
	}
	call(t, c, proto.MethodProjectRefresh, proto.ProjectRef{ID: proj.ID}, nil)
	p := waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Path == wt && w.Rebasing == "feat" && w.Status != nil && w.Status.Conflicts > 0 {
				return true
			}
		}
		return false
	})
	for _, w := range p.Worktrees {
		if w.Main && w.Rebasing != "" {
			t.Errorf("the main checkout is not rebasing: %+v", w)
		}
	}
}
