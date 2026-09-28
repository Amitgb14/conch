package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// taskFixture is a server with one project, whose agents run script as
// their login shell; task starts a task there, and worktree says where a
// branch's worktree goes.
func taskFixture(t *testing.T, script string) (c *client.Client, task func(branch, name string) (proto.PaneInfo, error), worktree func(string) string) {
	t.Helper()
	home := a5Env(t)
	sh := filepath.Join(home, "fake-shell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	c, dir := startServer(t)
	events := a5Collect(c)
	ctx := a5Ctx(t)

	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "project loaded", func(m proto.Message) bool {
		var p proto.ProjectInfo
		return m.Event == proto.EventProjectUpdated && json.Unmarshal(m.Data, &p) == nil && p.Base == "main"
	})
	task = func(branch, name string) (proto.PaneInfo, error) {
		var info proto.PaneInfo
		err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "review it",
			Branch: branch, Name: name, Cols: 60, Rows: 10}, &info)
		return info, err
	}
	worktree = func(branch string) string { return filepath.Join(dir, "api.worktrees", branch) }
	return c, task, worktree
}

// A task named as it starts: the name is the pane's own (as a rename
// makes it), refused while a running pane holds it or when it would read
// as a pane ID, and refused before any worktree is made.
func TestTaskName(t *testing.T) {
	// A login shell that keeps the agent's pane running, launching nothing.
	c, task, worktree := taskFixture(t, "exec sleep 30")
	ctx := a5Ctx(t)

	first, err := task("one", "  reviewer ")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "reviewer" || !first.CustomName || first.State != proto.PaneRunning {
		t.Fatalf("named task: %+v", first)
	}
	if first.Cwd != worktree("one") { // so the checks below look in the right place
		t.Fatalf("worktree at %s, not %s", first.Cwd, worktree("one"))
	}
	var list proto.PaneList
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	if len(list.Panes) != 1 || list.Panes[0].Name != "reviewer" {
		t.Fatalf("listed: %+v", list.Panes)
	}

	for _, c := range []struct{ branch, name, want string }{
		{"two", "reviewer", `pane ` + first.ID + ` is already named "reviewer"`},
		{"three", "p12", `"p12" is shaped like a pane ID`},
	} {
		_, err := task(c.branch, c.name)
		var perr *proto.Error
		if !asProtoErr(err, &perr) || perr.Code != proto.ErrBadRequest || !strings.Contains(perr.Message, c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if _, err := os.Stat(worktree(c.branch)); err == nil {
			t.Errorf("%s: a worktree was made for a refused name", c.name)
		}
	}

	// Unnamed tasks are as before, and a name comes free with its pane.
	if plain, err := task("four", ""); err != nil || plain.Name != "claude" || plain.CustomName {
		t.Fatalf("unnamed: %+v %v", plain, err)
	}
	if err := c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: first.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if again, err := task("five", "reviewer"); err != nil || again.Name != "reviewer" || again.ID == first.ID {
		t.Fatalf("name reused: %+v %v", again, err)
	}
}

func TestTaskNameEdges(t *testing.T) {
	c, task, _ := taskFixture(t, "exec sleep 30")
	ctx := a5Ctx(t)

	// A blank name is no name.
	if info, err := task("blank", "  \t "); err != nil || info.CustomName || info.Name != "claude" {
		t.Fatalf("blank: %+v %v", info, err)
	}
	// A pane that has ended leaves its name free.
	first, err := task("first", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	var ended proto.PaneInfo
	if err := syscallKill(first.PID); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		var list proto.PaneList
		c.Call(ctx, proto.MethodPaneList, nil, &list)
		for _, p := range list.Panes {
			if p.ID == first.ID {
				ended = p
			}
		}
		if ended.State == proto.PaneExited {
			break
		}
		if i == 250 {
			t.Fatalf("pane never ended: %+v", ended)
		}
		time.Sleep(20 * time.Millisecond)
	}
	second, err := task("second", "reviewer")
	if err != nil || second.Name != "reviewer" || second.ID == first.ID {
		t.Fatalf("after the first ended: %+v %v", second, err)
	}
	// Names are exact: another case is another name.
	if _, err := task("third", "Reviewer"); err != nil {
		t.Fatalf("other case: %v", err)
	}
	// Spaces and any letters are a name like another.
	if info, err := task("fourth", "code review ✓"); err != nil || info.Name != "code review ✓" {
		t.Fatalf("spaces: %+v %v", info, err)
	}
}

// An agent that fails at once still leaves its pane named, so the failure
// can be read by the name it was given.
func TestTaskNameAgentExitsAtOnce(t *testing.T) {
	c, task, _ := taskFixture(t, "echo no-agent-here; exit 1")
	ctx := a5Ctx(t)
	info, err := task("quick", "reviewer")
	if err != nil || info.Name != "reviewer" {
		t.Fatalf("named: %+v %v", info, err)
	}
	for i := 0; ; i++ {
		var list proto.PaneList
		c.Call(ctx, proto.MethodPaneList, nil, &list)
		if len(list.Panes) == 1 && list.Panes[0].State == proto.PaneExited {
			if list.Panes[0].Name != "reviewer" {
				t.Fatalf("ended pane lost its name: %+v", list.Panes[0])
			}
			return
		}
		if i == 250 {
			t.Fatalf("never ended: %+v", list.Panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
