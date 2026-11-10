package server_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// groupTaskFixture is a server with git projects spring and web in the
// group clinic and a plain folder notes, whose agents run under a login
// shell that writes what it was asked to run to log and then waits.
func groupTaskFixture(t *testing.T) (c *client.Client, dir, log string, ids map[string]string) {
	t.Helper()
	home := a5Env(t)
	log = filepath.Join(home, "ran")
	sh := filepath.Join(home, "fake-shell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" >> "+log+"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	c, dir = startServer(t)
	ctx := a5Ctx(t)
	ids = map[string]string{}
	for _, name := range []string{"spring", "web"} {
		repo := filepath.Join(dir, name)
		os.MkdirAll(repo, 0o755)
		git(t, repo, "init", "-q", "-b", "main")
		git(t, repo, "config", "user.name", "t")
		git(t, repo, "config", "user.email", "t@example.com")
		git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
		ids[name] = addFolder(t, c, dir, name).ID
	}
	ids["notes"] = addFolder(t, c, dir, "notes").ID
	// The projects' git state, read in the background: wait for it.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var pl proto.ProjectList
		if err := c.Call(ctx, proto.MethodProjectList, nil, &pl); err == nil &&
			!slices.ContainsFunc(pl.Projects, func(p proto.ProjectInfo) bool { return p.Name != "notes" && p.Base != "main" }) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "clinic", Projects: []string{ids["spring"], ids["web"]}, Notes: "API first"}, nil); err != nil {
		t.Fatal(err)
	}
	return c, dir, log, ids
}

func waitRan(t *testing.T, path string, has ...string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var b []byte
	for time.Now().Before(deadline) {
		b, _ = os.ReadFile(path)
		if !slices.ContainsFunc(has, func(s string) bool { return !strings.Contains(string(b), s) }) {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never had %q:\n%s", path, has, b)
	return ""
}

// One agent for the whole task: a worktree on the branch in each repo,
// a task folder linking them under the repos' names with CONCH-TASK.md,
// and the agent started there with every worktree added; the task kept
// with its group, and through a rename of the group.
func TestGroupTaskOneAgent(t *testing.T) {
	c, dir, log, ids := groupTaskFixture(t)
	ctx := a5Ctx(t)
	var res proto.GroupTaskResult
	if err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "Add patient search", Branch: "add-search", Agent: "claude", Cols: 80, Rows: 20}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Task.Branch != "add-search" || !slices.Equal(res.Task.Projects, []string{ids["spring"], ids["web"]}) || len(res.Panes) != 1 || res.Task.Each {
		t.Fatalf("%+v", res)
	}
	folder := res.Task.Folder
	if !strings.HasSuffix(folder, filepath.Join("tasks", "clinic", "add-search")) {
		t.Fatalf("folder %s", folder)
	}
	for _, name := range []string{"spring", "web"} {
		target, err := os.Readlink(filepath.Join(folder, name))
		want := filepath.Join(dir, name+".worktrees", "add-search")
		if err != nil || target != want {
			t.Fatalf("%s links to %q (%v), want %s", name, target, err, want)
		}
		if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
			t.Fatalf("no worktree at %s: %v", want, err)
		}
	}
	brief, _ := os.ReadFile(filepath.Join(folder, "CONCH-TASK.md"))
	for _, want := range []string{"Add patient search", "`spring/`", "`web/`", "API first", "add-search"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("CONCH-TASK.md has no %q:\n%s", want, brief)
		}
	}
	ran := waitRan(t, log, "--add-dir", filepath.Join(dir, "web.worktrees", "add-search"))
	if !strings.HasPrefix(ran, folder+"\n") || !strings.Contains(ran, filepath.Join(dir, "spring.worktrees", "add-search")) || !strings.Contains(ran, "CONCH-TASK.md") {
		t.Fatalf("the agent ran as:\n%s", ran)
	}
	if res.Panes[0].ProjectID != "" {
		t.Errorf("the task's agent joined project %s", res.Panes[0].ProjectID)
	}
	// Listed with its group; a rename keeps it.
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "clinic app", Old: "clinic", Projects: []string{ids["spring"], ids["web"]}}, nil); err != nil {
		t.Fatal(err)
	}
	got := listGroups(t, c)
	if len(got) != 1 || len(got[0].Tasks) != 1 || got[0].Tasks[0].Branch != "add-search" || !slices.Equal(got[0].Tasks[0].Panes, []string{res.Panes[0].ID}) {
		t.Fatalf("listed %+v", got)
	}

	// Removed: the folder goes, the worktrees and branches stay.
	if err := c.Call(ctx, proto.MethodGroupTaskRemove, proto.GroupTaskRef{Group: "clinic app", Branch: "add-search"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web.worktrees", "add-search", ".git")); err != nil {
		t.Fatalf("removing the task took a worktree: %v", err)
	}
	if got := listGroups(t, c); len(got[0].Tasks) != 0 {
		t.Fatalf("still listed %+v", got[0].Tasks)
	}
	if err := c.Call(ctx, proto.MethodGroupTaskRemove, proto.GroupTaskRef{Group: "clinic app", Branch: "add-search"}, nil); err == nil {
		t.Error("removed twice")
	}
}

// An agent per repo: each in its own worktree, told about the others; a
// subset of the repos when asked for.
func TestGroupTaskEach(t *testing.T) {
	c, dir, log, ids := groupTaskFixture(t)
	ctx := a5Ctx(t)
	var res proto.GroupTaskResult
	if err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "Bump the version", Branch: "bump", Agent: "claude", Each: true}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Panes) != 2 || !res.Task.Each {
		t.Fatalf("%+v", res)
	}
	if res.Panes[0].ProjectID != ids["spring"] || res.Panes[1].ProjectID != ids["web"] {
		t.Errorf("panes in %s and %s", res.Panes[0].ProjectID, res.Panes[1].ProjectID)
	}
	waitRan(t, log, filepath.Join(dir, "spring.worktrees", "bump")+"\n", filepath.Join(dir, "web.worktrees", "bump")+"\n", "other agents")

	if err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "Only the web", Branch: "web-only", Agent: "claude", Projects: []string{ids["web"]}}, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Task.Projects, []string{ids["web"]}) {
		t.Fatalf("%+v", res.Task)
	}
	if _, err := os.Stat(filepath.Join(dir, "spring.worktrees", "web-only")); !os.IsNotExist(err) {
		t.Fatalf("a repo not asked for got a worktree: %v", err)
	}
}

// What is refused, before anything is made: no prompt, no such group, a
// repo not in it, a plain folder in it, a task already on the branch,
// and a branch one repo has checked out already — which must leave no
// worktree in the other.
func TestGroupTaskRefuses(t *testing.T) {
	c, dir, _, ids := groupTaskFixture(t)
	ctx := a5Ctx(t)
	if err := c.Call(ctx, proto.MethodGroupSave, proto.GroupSaveParams{Name: "mixed", Projects: []string{ids["spring"], ids["notes"]}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []proto.GroupTaskParams{
		{Group: "clinic", Prompt: "  "},
		{Group: "nope", Prompt: "x"},
		{Group: "clinic", Prompt: "x", Projects: []string{ids["notes"]}},
		{Group: "mixed", Prompt: "x", Branch: "m"},
		{Group: "clinic", Prompt: "x", Branch: "bad..name"},
		{Group: "clinic", Prompt: "x", Agent: "no-such-agent"},
	} {
		if err := c.Call(ctx, proto.MethodGroupTask, p, nil); err == nil {
			t.Errorf("%+v was taken", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "spring.worktrees", "m")); !os.IsNotExist(err) {
		t.Fatalf("a refused task left a worktree: %v", err)
	}
	// web has "taken" checked out already: refused — by the check before
	// anything is made, or, while the server has not seen that worktree
	// yet, by git, with spring's worktree undone — and spring keeps none.
	git(t, filepath.Join(dir, "web"), "worktree", "add", "-q", "-b", "taken", filepath.Join(dir, "elsewhere"))
	err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "x", Branch: "taken", Agent: "claude"}, nil)
	if err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("a branch checked out elsewhere: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spring.worktrees", "taken")); !os.IsNotExist(err) {
		t.Fatalf("half a task was made: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "clinic", "taken")); !os.IsNotExist(err) {
		t.Fatalf("a refused task left its folder: %v", err)
	}
	// A second task on a branch the group has a task on already.
	if err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "x", Branch: "twice", Agent: "claude"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodGroupTask, proto.GroupTaskParams{Group: "clinic", Prompt: "x", Branch: "twice", Agent: "claude"}, nil); err == nil {
		t.Error("a second task on the same branch was taken")
	}
}
