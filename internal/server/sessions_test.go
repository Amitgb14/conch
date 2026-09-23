package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func TestSessionsInterruptedAndResume(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", "/usr/bin:/bin") // no real agents

	dir, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)

	// Two Claude sessions saved for the folder; the older one was running in
	// conch when its server went away.
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, repo)
	for id, text := range map[string]string{"old": "Fix login", "new": "Write docs"} {
		p := filepath.Join(home, ".claude", "projects", enc, id+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o755)
		line, _ := json.Marshal(map[string]any{"type": "user", "cwd": repo, "message": map[string]any{"role": "user", "content": text}})
		os.WriteFile(p, append(line, '\n'), 0o644)
		when := time.Now().Add(-2 * time.Hour)
		if id == "new" {
			when = time.Now().Add(-time.Hour)
		}
		os.Chtimes(p, when, when)
	}
	runs, _ := json.Marshal(map[string]any{"active": []map[string]any{{
		"pane": "p1", "agent": "claude", "dir": repo, "session": "old",
		"started": time.Now().Add(-3 * time.Hour), "seen": time.Now().Add(-2 * time.Hour),
	}}})
	os.WriteFile(filepath.Join(dir, "agent-runs.json"), runs, 0o600)

	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	defer func() { srv.Stop(); time.Sleep(100 * time.Millisecond) }()
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var list proto.SessionList
	if err := c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: repo}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 2 || list.Sessions[0].ID != "old" || !list.Sessions[0].Interrupted || list.Sessions[1].Interrupted {
		t.Fatalf("interrupted session should come first: %+v", list.Sessions)
	}

	var info proto.PaneInfo
	ref := proto.SessionRef{Agent: "claude", ID: "old", Dir: repo, Cols: 80, Rows: 24}
	if err := c.Call(ctx, proto.MethodSessionResume, ref, &info); err != nil {
		t.Fatal(err)
	}
	if info.Cwd != repo || !strings.Contains(strings.Join(info.Command, " "), "--resume old") {
		t.Fatalf("resume pane: %+v", info)
	}
	list = proto.SessionList{}
	c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: repo}, &list)
	for _, s := range list.Sessions {
		if s.Interrupted {
			t.Fatalf("resuming should clear the interrupted mark: %+v", s)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "agent-runs.json"))
	if strings.Contains(string(b), `"old"`) {
		t.Fatalf("run log still has the resumed run: %s", b)
	}

	// Deleting: an open session is refused, another one is removed.
	if err := c.Call(ctx, proto.MethodSessionDelete, proto.SessionRef{Agent: "claude", ID: "nope", Dir: repo}, nil); err == nil {
		t.Fatal("deleting an unknown session should fail")
	}
	if err := c.Call(ctx, proto.MethodSessionDelete, proto.SessionRef{Agent: "claude", ID: "new", Dir: repo}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects", enc, "new.jsonl")); !os.IsNotExist(err) {
		t.Fatal("deleted session file still in place")
	}
	if moved, _ := filepath.Glob(filepath.Join(dir, "trash", "claude-*-new.jsonl")); len(moved) != 1 {
		t.Fatalf("deleted session not in conch's trash: %v", moved)
	}

	ref.Dir = filepath.Join(dir, "gone")
	if err := c.Call(ctx, proto.MethodSessionResume, ref, &info); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("resuming in a missing folder: %v", err)
	}
}

// TestSessionListReportsCost checks that a session's cost reaches the
// client, from the store and — for one open in a pane — from the pane's
// live usage, which is ahead of it.
func TestSessionListReportsCost(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	dir, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, repo)
	p := filepath.Join(home, ".claude", "projects", enc, "s1.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "user", "cwd": repo, "message": map[string]any{"role": "user", "content": "Fix the tests"}})
	body := append(line, []byte("\n{\"type\":\"cost-state\",\"totalCostUSD\":0.25}\n")...)
	os.WriteFile(p, body, 0o644)

	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	defer func() { srv.Stop(); time.Sleep(100 * time.Millisecond) }()
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c == nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var list proto.SessionList
	if err := c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: repo}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].CostUSD != 0.25 {
		t.Fatalf("cost from the store: %+v", list.Sessions)
	}
}

// TestSessionListPartialWhenAnAgentIsSlow checks that a list says so when an
// agent whose sessions come from another program (Devin's CLI) has not
// answered yet, and still carries the agents whose sessions are files.
func TestSessionListPartialWhenAnAgentIsSlow(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("CONCH_SESSION_GRACE", "50ms")
	dir, err := os.MkdirTemp("", "cp")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)

	// A devin that never answers in time. It is under HOME, which is looked
	// at before PATH, so no real devin is ever run.
	devin := filepath.Join(home, ".local", "bin", "devin")
	os.MkdirAll(filepath.Dir(devin), 0o755)
	if err := os.WriteFile(devin, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, repo)
	p := filepath.Join(home, ".claude", "projects", enc, "s1.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "user", "cwd": repo, "message": map[string]any{"role": "user", "content": "Fix the tests"}})
	os.WriteFile(p, append(line, '\n'), 0o644)

	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	defer func() { srv.Stop(); time.Sleep(100 * time.Millisecond) }()
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c == nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var list proto.SessionList
	start := time.Now()
	if err := c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: repo}, &list); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("the list waited %v for devin", took)
	}
	if !list.Partial {
		t.Error("want the list marked partial while devin is slow")
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Agent != "claude" {
		t.Fatalf("want the claude session anyway, got %+v", list.Sessions)
	}
}
