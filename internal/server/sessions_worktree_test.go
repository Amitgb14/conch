package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestSessionListTakesWorktrees: a task's agent works in the branch's
// worktree, and Claude keeps that conversation under the worktree's own
// path — so a project's sessions are its main checkout's and every
// worktree's. Seen in Conch.app: a task's two conversations, and no
// Sessions row for the project.
func TestSessionListTakesWorktrees(t *testing.T) {
	gitInitIdentity(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := filepath.Join(dir, "app")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "task"}, &wt); err != nil {
		t.Fatal(err)
	}
	if wt.Path == "" {
		t.Fatal("no worktree path")
	}
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, wt.Path)
	p := filepath.Join(home, ".claude", "projects", enc, "s1.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "user", "cwd": wt.Path, "gitBranch": "task",
		"message": map[string]any{"role": "user", "content": "Build the screen"}})
	os.WriteFile(p, append(line, '\n'), 0o644)

	var list proto.SessionList
	if err := c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{ProjectID: proj.ID, Limit: 500}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Dir != wt.Path {
		t.Fatalf("worktree's session: %+v", list.Sessions)
	}
}
