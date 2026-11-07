package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relocate"
	"github.com/Amitgb14/conch/internal/server"
)

func moveGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// startMoveServer runs a real server on sock, stopping it when the test ends.
func startMoveServer(t *testing.T, sock, state string) {
	t.Helper()
	srv := server.New(sock, state)
	stopped := make(chan struct{})
	go func() { srv.Run(); close(stopped) }()
	// Stop only asks: the server shuts down — saving its state, finishing
	// a git call — after it returns, so wait, or the test's folders are
	// removed while it is still writing in them (seen on Linux).
	t.Cleanup(func() {
		srv.Stop()
		select {
		case <-stopped:
		case <-time.After(30 * time.Second):
			t.Error("the server did not stop")
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(sock, "test"); err == nil {
			c.Close()
			return
		}
		if i == 300 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// conch project move, between two real servers: -plan says what would
// move and changes nothing; -json -y moves it, a line of JSON a step.
func TestProjectMoveCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := a4Env(t)
	home := os.Getenv("HOME")
	gitconfig := filepath.Join(home, ".gitconfig")
	os.WriteFile(gitconfig, []byte("[user]\n\tname = Test\n\temail = test@example.com\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	origin := filepath.Join(home, "origin.git")
	moveGit(t, home, "init", "-q", "--bare", "-b", "main", origin)
	src := filepath.Join(home, "workspace", "daos")
	moveGit(t, home, "clone", "-q", origin, src)
	src, _ = filepath.EvalSymlinks(src) // the server records real paths
	moveGit(t, src, "commit", "-q", "--allow-empty", "-m", "first")
	moveGit(t, src, "push", "-q", "origin", "HEAD:main")

	startMoveServer(t, config.SocketPath(), filepath.Join(dir, "here"))
	thereDir, _ := os.MkdirTemp("", "mv")
	t.Cleanup(func() { os.RemoveAll(thereDir) })
	thereSock := filepath.Join(thereDir, "s")
	startMoveServer(t, thereSock, filepath.Join(thereDir, "state"))
	old := connectMoveTarget
	t.Cleanup(func() { connectMoveTarget = old })
	connectMoveTarget = func(ref string) (*client.Client, error) {
		if ref != "there" {
			t.Errorf("connected to %q", ref)
		}
		return client.Dial(thereSock, "test")
	}

	c, err := client.Dial(config.SocketPath(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var info proto.ProjectInfo
	if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: src}, &info); err != nil {
		t.Fatal(err)
	}
	var wt proto.WorktreeResult
	if err := call(c, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: info.ID, Branch: "feat/fix"}, &wt); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt.Path, "notes.txt"), []byte("untracked\n"), 0o644)
	for i := 0; ; i++ {
		var list proto.ProjectList
		call(c, proto.MethodProjectList, nil, &list)
		if len(list.Projects) == 1 && len(relocate.ProjectWorktrees(&list.Projects[0])) == 1 {
			break
		}
		if i == 200 {
			t.Fatal("the worktree never showed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	run := func(args ...string) (string, error) {
		t.Helper()
		var err error
		out, _ := a4Capture(t, "", func() { err = runProject(append([]string{"move"}, args...)) })
		return out, err
	}

	// -plan: what would move, nothing moved.
	out, err := run("-plan", "daos", "-to", "there", "-into", "~/there/daos")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	for _, want := range []string{"Move daos (" + src + ") to there", "clone    ", "into ~/there/daos there", "worktree feat/fix · 1 untracked"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan says no %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "there", "daos")); err == nil {
		t.Fatal("-plan cloned")
	}

	// -json -y: the plan, each step, the result.
	out, err = run("daos", "-to", "there", "-into", "~/there/daos", "-json", "-y")
	if err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	var kinds []string
	var result relocate.Result
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var line map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("not a line of JSON: %q", sc.Text())
		}
		for k, v := range line {
			if k == "steps" { // beside the plan, on its line
				continue
			}
			kinds = append(kinds, k)
			if k == "event" {
				var e relocate.Event
				json.Unmarshal(v, &e)
				kinds[len(kinds)-1] = "event:" + e.Kind + ":" + e.State
			}
			if k == "result" {
				json.Unmarshal(v, &result)
			}
		}
	}
	got := strings.Join(kinds, " ")
	if !strings.HasPrefix(got, "plan") || !strings.Contains(got, "event:clone:ok") || !strings.Contains(got, "event:worktree:ok") || !strings.HasSuffix(got, "result") {
		t.Fatalf("lines: %s", got)
	}
	realHome, _ := filepath.EvalSymlinks(home)
	if strings.Join(result.Moved, ",") != "feat/fix" || len(result.Failed) > 0 || result.TargetPath != filepath.Join(realHome, "there", "daos") {
		t.Fatalf("result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(home, "there", "daos.worktrees", "feat-fix", "notes.txt")); err != nil {
		t.Fatalf("the worktree's untracked file isn't there: %v", err)
	}

	// What it refuses, before reaching anything.
	for args, want := range map[string]string{
		"daos":                       projectMoveUsage,
		"-to there":                  projectMoveUsage,
		"daos -to there -json":       "add -y to move, or -plan",
		"daos -to local":             "already on local",
		"daos -to there -what":       "flag provided but not defined",
		"daos extra -to there -plan": projectMoveUsage,
	} {
		if _, err := run(strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", args, err, want)
		}
	}
	// A project it doesn't know, said as JSON too.
	out, err = run("nope", "-to", "there", "-json", "-plan")
	if err == nil || !strings.Contains(out, `"error":"no project \"nope\" on local"`) {
		t.Fatalf("unknown project: %v %q", err, out)
	}
}
