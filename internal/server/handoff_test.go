package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Two servers stand in for two machines: a conversation exported on one is
// shared with an agent on the other, over their sockets as the TUI does.
func TestSessionHandoffBetweenServers(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "CONCH_SOCKET", "CONCH_PANE_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin") // no real agents
	record := filepath.Join(home, "argv")
	sh := filepath.Join(home, "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" > "+record+"\nsleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)

	laptop, ldir := startServer(t)
	remote, rdir := startServer(t)
	for _, c := range []interface{ MissingCapabilities([]string) []string }{laptop, remote} {
		if missing := c.MissingCapabilities([]string{proto.CapSessionHandoff}); len(missing) > 0 {
			t.Fatalf("missing %v", missing)
		}
	}

	// A Claude session in a checkout on the laptop.
	work := filepath.Join(ldir, "api")
	os.MkdirAll(work, 0o755)
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, work)
	p := filepath.Join(home, ".claude", "projects", enc, "c1.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"type":"user","cwd":"`+work+`","timestamp":"2026-09-14T10:00:00Z","message":{"role":"user","content":"Why does deploy fail on arm64?"}}`+"\n"+
		`{"type":"assistant","cwd":"`+work+`","message":{"content":[{"type":"text","text":"The cross compiler is missing."}]}}`+"\n"), 0o644)
	// The same project checked out elsewhere on the remote.
	there := filepath.Join(rdir, "api")
	os.MkdirAll(there, 0o755)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var exp proto.SessionExport
	if err := laptop.Call(ctx, proto.MethodSessionExport, proto.SessionRef{Agent: "claude", ID: "c1", Dir: work}, &exp); err != nil {
		t.Fatal(err)
	}
	if exp.Name != "claude-c1.md" || !strings.Contains(exp.Doc, "The cross compiler is missing.") {
		t.Fatalf("export: %+v", exp)
	}
	// The remote has no such session, so without the document it can't share.
	var res proto.SessionShareResult
	if err := remote.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: there, To: "codex"}, &res); err == nil {
		t.Fatal("shared a session the remote doesn't have")
	}
	if err := remote.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: there, To: "codex",
		Doc: exp.Doc, Name: exp.Name, From: "laptop", Cols: 80, Rows: 24}, &res); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(there, ".conch", "handoff", "claude-c1.md")); err != nil || string(b) != exp.Doc || res.Pane.ID == "" {
		t.Fatalf("handoff on the remote: %v %+v", err, res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(record)
		if strings.Contains(string(b), "earlier Claude Code session on laptop") {
			if !strings.HasPrefix(string(b), there+"\n") || !strings.Contains(string(b), "codex") {
				t.Fatalf("started as:\n%s", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never started; argv:\n%s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Nothing was written on the laptop.
	if _, err := os.Stat(filepath.Join(work, ".conch")); err == nil {
		t.Fatal("export wrote a handoff on the laptop")
	}
}
