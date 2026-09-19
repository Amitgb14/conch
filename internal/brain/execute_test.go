package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func TestExecuteSendAndClose(t *testing.T) {
	dir, err := os.MkdirTemp("", "cb") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	srv := server.New(filepath.Join(dir, "s.sock"), dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(filepath.Join(dir, "s.sock"), "test"); err == nil {
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
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: dir}, &info); err != nil {
		t.Fatal(err)
	}
	var list proto.PaneList
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	w := brain.World{Machines: []brain.Machine{brain.MachineFrom("local", "local", true, nil, nil, list.Panes, nil)}}

	send := brain.Action{Type: brain.ActSend, Machine: "local", Pane: info.ID, Text: "hello brain"}
	if err := w.Validate(&send); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Execute(ctx, c, send, 80, 24); err != nil {
		t.Fatal(err)
	}
	for {
		var screen proto.PaneReadResult
		c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen)
		if strings.Count(strings.Join(screen.Lines, "\n"), "hello brain") >= 2 { // typed, then echoed by cat
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("text never arrived: %q", screen.Lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := brain.Execute(ctx, c, brain.Action{Type: brain.ActClose, Machine: "local", Pane: info.ID}, 0, 0); err != nil {
		t.Fatal(err)
	}
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == info.ID {
			t.Fatal("pane still open")
		}
	}
}

// startBrainServer is a real server with a client, for executing actions.
func startBrainServer(t *testing.T) (*client.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cb")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	srv := server.New(filepath.Join(dir, "s.sock"), dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(filepath.Join(dir, "s.sock"), "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			return c, dir
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, ""
}

// TestExecuteBroadcastAndShare runs both new actions against a real
// server. The agents are fake scripts that echo what is typed into them.
func TestExecuteBroadcastAndShare(t *testing.T) {
	// The fakes go where every adapter prepends to PATH inside the command
	// it runs ($HOME/.local/bin), not just on this process's PATH: agents
	// start through a login shell, and some of those replace PATH outright
	// (Debian's /etc/profile does, so the agents went missing there while
	// macOS and Ubuntu kept them).
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nstty -echo; exec cat\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("HOME", home)
	c, dir := startBrainServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var a, b, shell proto.PaneInfo
	for _, p := range []struct {
		info  *proto.PaneInfo
		agent string
	}{{&a, "claude"}, {&b, "codex"}, {&shell, ""}} {
		params := proto.PaneCreateParams{Agent: p.agent, Cwd: dir, Cols: 120, Rows: 24}
		if p.agent == "" {
			params.Command = []string{"/bin/sh", "-c", "stty -echo; exec cat"}
		}
		if err := c.Call(ctx, proto.MethodPaneCreate, params, p.info); err != nil {
			t.Fatal(err)
		}
	}

	// The server notices what a pane is running on its own schedule.
	waitAgents(t, ctx, c, a.ID, b.ID)

	// Broadcast reaches both agents.
	act := brain.Action{Type: brain.ActBroadcast, Machine: "local", Panes: []string{a.ID, b.ID}, Text: "run the tests"}
	if _, err := brain.Execute(ctx, c, act, 80, 24); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		waitScreenText(t, ctx, c, id, "run the tests")
	}

	// A terminal is never a recipient: the server skips it, and Execute
	// says so rather than reporting success.
	toShell := brain.Action{Type: brain.ActBroadcast, Machine: "local", Panes: []string{shell.ID}, Text: "rm -rf /"}
	if _, err := brain.Execute(ctx, c, toShell, 80, 24); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("broadcast to a terminal: %v", err)
	}
	// Some sent, some not, is reported too.
	mixed := brain.Action{Type: brain.ActBroadcast, Machine: "local", Panes: []string{a.ID, shell.ID}, Text: "status please"}
	if _, err := brain.Execute(ctx, c, mixed, 80, 24); err == nil || !strings.Contains(err.Error(), "sent to 1 of 2") {
		t.Fatalf("partial broadcast: %v", err)
	}

	// Share: the agent has saved no conversation yet, and a pane that is
	// gone or is a terminal says why.
	share := brain.Action{Type: brain.ActShare, Machine: "local", Pane: a.ID, Agent: "codex"}
	if _, err := brain.Execute(ctx, c, share, 80, 24); err == nil || !strings.Contains(err.Error(), "hasn't saved a conversation") {
		t.Fatalf("share without a session: %v", err)
	}
	fromShell := brain.Action{Type: brain.ActShare, Machine: "local", Pane: shell.ID, Agent: "codex"}
	if _, err := brain.Execute(ctx, c, fromShell, 80, 24); err == nil || !strings.Contains(err.Error(), "not running an agent") {
		t.Fatalf("share from a terminal: %v", err)
	}
	missing := brain.Action{Type: brain.ActShare, Machine: "local", Pane: "nope", Agent: "codex"}
	if _, err := brain.Execute(ctx, c, missing, 80, 24); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("share from a missing pane: %v", err)
	}
}

// waitScreenText polls a pane until want shows on its screen.
func waitScreenText(t *testing.T, ctx context.Context, c *client.Client, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var screen proto.PaneReadResult
		if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: id}, &screen); err == nil {
			if strings.Contains(strings.Join(screen.Lines, "\n"), want) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s never showed %q", id, want)
}

// waitAgents waits until the server reports each pane as running an agent.
func waitAgents(t *testing.T, ctx context.Context, c *client.Client, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var list proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err == nil {
			seen := map[string]bool{}
			for _, p := range list.Panes {
				if p.Agent != nil {
					seen[p.ID] = true
				}
			}
			all := true
			for _, id := range ids {
				all = all && seen[id]
			}
			if all {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the server never reported the panes as agents")
}
