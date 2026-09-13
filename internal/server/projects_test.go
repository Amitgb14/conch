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

	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/proto"
	"github.com/amitghadge/conch/internal/server"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func startServer(t *testing.T) (*client.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "conch")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			return c, dir
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, ""
}

// waitProject waits for a project.updated event satisfying ok.
func waitProject(t *testing.T, c *client.Client, ok func(proto.ProjectInfo) bool) proto.ProjectInfo {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, open := <-c.Events:
			if !open {
				t.Fatalf("connection closed: %v", c.Err())
			}
			var p proto.ProjectInfo
			if m.Event == proto.EventProjectUpdated && json.Unmarshal(m.Data, &p) == nil && ok(p) {
				return p
			}
		case <-timeout:
			t.Fatal("timed out waiting for project update")
		}
	}
}

func TestProjectsAndWorktrees(t *testing.T) {
	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A pane opened inside the repo adds it as a project.
	var info proto.PaneInfo
	err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: filepath.Join(repo),
	}, &info)
	if err != nil {
		t.Fatal(err)
	}
	proj := waitProject(t, c, func(p proto.ProjectInfo) bool { return p.Path == repo && len(p.Branches) > 0 })
	if proj.Base != "main" || proj.Branches[0].Name != "main" || len(proj.Worktrees) != 1 {
		t.Fatalf("unexpected project: %+v", proj)
	}

	// A worktree for a new branch lands next to the repo.
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat/x"}, &wt); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "api.worktrees", "feat-x"); wt.Path != want {
		t.Fatalf("worktree at %s, want %s", wt.Path, want)
	}
	os.WriteFile(filepath.Join(wt.Path, "a.txt"), []byte("one\ntwo\n"), 0o644)
	proj = waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Branch == "feat/x" && w.Status != nil && w.Status.Added == 1 {
				return true
			}
		}
		return false
	})

	var ch proto.Changes
	if err := c.Call(ctx, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: proj.ID, Branch: "feat/x"}, &ch); err != nil {
		t.Fatal(err)
	}
	if ch.Worktree != wt.Path || len(ch.Files) != 1 || ch.Files[0].Path != "a.txt" || ch.Files[0].Added != 1 {
		t.Fatalf("changes: %+v", ch)
	}
	var d proto.DiffResult
	if err := c.Call(ctx, proto.MethodProjectDiff, proto.DiffParams{ProjectID: proj.ID, Branch: "feat/x", File: "a.txt"}, &d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Diff, "+two") {
		t.Fatalf("diff: %s", d.Diff)
	}

	// A dirty worktree is not removed, and the main worktree never is.
	err = c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: wt.Path}, nil)
	if err == nil {
		t.Fatal("dirty worktree was removed")
	}
	err = c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: repo}, nil)
	if err == nil {
		t.Fatal("main worktree was removed")
	}

	// The pane in the repo reports its project and branch.
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	if p := list.Panes[0]; p.ProjectID != proj.ID || p.Branch != "main" {
		t.Fatalf("pane placement: project %q branch %q", p.ProjectID, p.Branch)
	}

	// Rename sticks; an empty name restores the default.
	var renamed proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: "server"}, &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "server" || !renamed.CustomName {
		t.Fatalf("rename: %+v", renamed)
	}

	// The catalog survives in projects.json.
	b, err := os.ReadFile(filepath.Join(dir, "projects.json"))
	if err != nil || !strings.Contains(string(b), repo) {
		t.Fatalf("catalog: %s %v", b, err)
	}
}

func TestPullRequestsOnBranches(t *testing.T) {
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
cat <<'JSON'
[{"number":7,"title":"Ship it","state":"OPEN","isDraft":false,"url":"https://github.com/o/r/pull/7",
  "headRefName":"main","reviewDecision":"APPROVED","updatedAt":"2026-09-12T10:00:00Z",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}]}]
JSON
`), 0o755)
	t.Setenv("CONCH_GH", gh)

	c, dir := startServer(t)
	repo := filepath.Join(dir, "web")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, nil); err != nil {
		t.Fatal(err)
	}
	proj := waitProject(t, c, func(p proto.ProjectInfo) bool {
		return len(p.Branches) > 0 && p.Branches[0].PR != nil
	})
	pr := proj.Branches[0].PR
	if pr.Number != 7 || pr.Checks != "fail" || pr.Review != "APPROVED" || proj.PRStatus != "" {
		t.Fatalf("pr: %+v status %q", pr, proj.PRStatus)
	}
}

func TestBrowseAndCreateProjects(t *testing.T) {
	c, dir := startServer(t)
	os.MkdirAll(filepath.Join(dir, "src", "api", ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "Notes"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "file.txt"), []byte("x"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var list proto.FSList
	if err := c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src")}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[0].Name != "api" || !list.Entries[0].Git || list.Entries[1].Name != "Notes" || list.Parent != dir {
		t.Fatalf("listing: %+v", list)
	}
	if err := c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src"), Hidden: true}, &list); err != nil || len(list.Entries) != 3 {
		t.Fatalf("hidden listing: %+v %v", list.Entries, err)
	}

	if err := c.Call(ctx, proto.MethodFSMkdir, proto.FSMkdirParams{Path: filepath.Join(dir, "src", "new")}, &list); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodFSMkdir, proto.FSMkdirParams{Path: filepath.Join(dir, "src", "new")}, nil); err == nil {
		t.Fatal("mkdir over an existing folder succeeded")
	}

	var proj proto.ProjectInfo
	target := filepath.Join(dir, "src", "fresh")
	if err := c.Call(ctx, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: target, Git: true}, &proj); err != nil {
		t.Fatal(err)
	}
	if proj.Path != target || !proj.Git {
		t.Fatalf("created: %+v", proj)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatal("no git repository created")
	}
	if err := c.Call(ctx, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: target}, nil); err == nil {
		t.Fatal("created a project over an existing folder")
	}
	// The listing now marks it as a project.
	c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src")}, &list)
	for _, e := range list.Entries {
		if e.Name == "fresh" && !e.Project {
			t.Fatalf("fresh not marked as project: %+v", list.Entries)
		}
	}
}
