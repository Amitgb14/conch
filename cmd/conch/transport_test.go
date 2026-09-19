package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// a4Machine sets up a pretend machine reached by running commands locally:
// its own HOME, where the probe finds a conch that is this test binary in
// disguise, and its own CONCH_HOME for the server that conch starts there.
// It returns the transport and the machine's CONCH_HOME.
func a4Machine(t *testing.T, name string) (remote.Transport, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "m") // short: the server's socket lives under it
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	binDir := filepath.Join(dir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "conch")
	script := fmt.Sprintf("#!/bin/sh\nexec env A4_HELPER_MODE=main CONCH_HOME=%s CONCH_SOCKET=%s %s \"$@\"\n",
		home, filepath.Join(home, "s.sock"), exe)
	if err := os.WriteFile(filepath.Join(binDir, "conch"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir) // where the probe script looks for conch
	return remote.Exec(name, "/bin/sh", "-c"), home
}

// The whole remote path — probe, capability check, bridge, protocol — over
// a transport that only runs local commands. This is what a sandbox will
// use instead of ssh, and it gives the ssh-shaped code a test without ssh.
func TestA4ExecTransportRunsTheRemotePath(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a server")
	}
	a4Env(t)
	tr, home := a4Machine(t, "sandbox demo")
	if tr.Describe() != "sandbox demo" {
		t.Fatalf("describe %q", tr.Describe())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	probe, err := remote.ProbeMachine(ctx, tr)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.Platform == "" || probe.Bin == "" || probe.Info == nil {
		t.Fatalf("probe %+v", probe)
	}
	if missing := probe.Missing(); len(missing) > 0 {
		t.Fatalf("this build lacks its own capabilities: %v", missing)
	}

	var said []string
	c, err := remote.Connect(ctx, tr, remote.Options{Progress: func(s string) { said = append(said, s) }})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		_ = c.Call(context.Background(), proto.MethodServerStop, nil, nil) // take its server with us
		c.Close()
	}()
	if got := strings.Join(said, "; "); !strings.Contains(got, "probing sandbox demo") {
		t.Fatalf("progress %q", got)
	}
	if c.Server.PID == 0 {
		t.Fatalf("server %+v", c.Server)
	}

	// A pane on the far side, through the bridge.
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Name: "hi", Command: []string{"/bin/sh", "-c", "echo over-the-bridge; sleep 30"}, Cols: 80, Rows: 24,
	}, &info); err != nil {
		t.Fatalf("pane.create: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var screen proto.PaneReadResult
		if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen); err != nil {
			t.Fatalf("pane.read: %v", err)
		}
		if strings.Contains(strings.Join(screen.Lines, "\n"), "over-the-bridge") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never printed: %q", screen.Lines)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The server really is the machine's own, not this test's.
	if _, err := os.Stat(filepath.Join(home, "s.sock")); err != nil {
		t.Fatalf("no socket in the machine's own CONCH_HOME: %v", err)
	}
}

// An exec transport reports what went wrong on the machine, and refuses to
// run with nothing to run.
func TestA4ExecTransportErrors(t *testing.T) {
	a4Env(t)
	ctx := context.Background()
	if _, err := remote.ProbeMachine(ctx, remote.Exec("empty")); err == nil || !strings.Contains(err.Error(), "no command to run things with") {
		t.Fatalf("empty argv: %v", err)
	}
	if _, err := remote.ProbeMachine(ctx, remote.Exec("gone", "/definitely/not/here")); err == nil {
		t.Fatal("a missing command should fail")
	}
	_, err := remote.ProbeMachine(ctx, remote.Exec("noisy", "/bin/sh", "-c", "echo boom >&2; exit 1;"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not reported: %v", err)
	}
	// A machine that answers, but not like a machine conch can use.
	if _, err := remote.ProbeMachine(ctx, remote.Exec("odd", "/bin/sh", "-c", "echo Plan9; echo vax; :")); err == nil ||
		!strings.Contains(err.Error(), "unsupported CPU") {
		t.Fatalf("odd machine: %v", err)
	}
}
