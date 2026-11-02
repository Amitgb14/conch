package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// conch ssh starts the very session the TUI's H does: ssh with conch's
// own config and a connection of its own, the host last after --, in the
// home directory and no project; it prints the pane's ID.
func TestSSHLoginStartsALoginPane(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var mu sync.Mutex
	var got proto.PaneCreateParams
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneCreate {
			mu.Lock()
			json.Unmarshal(msg.Params, &got)
			mu.Unlock()
			return proto.PaneInfo{ID: "p9", Name: got.Name, Command: got.Command}, nil
		}
		return nil, nil
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runSSHLogin([]string{"me@build-box", "-p", "2222"}) })
	if err != nil || strings.TrimSpace(out) != "p9" {
		t.Fatalf("%v %q", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	n := len(got.Command)
	if n < 4 || filepath.Base(got.Command[0]) != "ssh" || got.Command[n-2] != "--" || got.Command[n-1] != "me@build-box" {
		t.Fatalf("command %q", got.Command)
	}
	if !remote.IsLoginCommand(got.Command) || !strings.Contains(strings.Join(got.Command, " "), "-p 2222") {
		t.Fatalf("not a login the TUI reads as one: %q", got.Command)
	}
	if !got.NoProject || got.Name != "me@build-box" || got.Cwd == "" {
		t.Fatalf("params %+v", got)
	}
}

// What it refuses: no target, a target that is an option, options ssh's
// login does not take, and another machine.
func TestSSHLoginRefuses(t *testing.T) {
	a4Env(t)
	for _, args := range [][]string{nil, {"-p", "22"}} {
		if err := runSSHLogin(args); err == nil || !strings.Contains(err.Error(), sshUsage) {
			t.Errorf("%q: %v", args, err)
		}
	}
	if err := runSSHLogin([]string{"me@box", "-F", "/tmp/x"}); err == nil {
		t.Error("an -F was taken")
	}
	machineFlag = "busybox"
	defer func() { machineFlag = "" }()
	if err := runSSHLogin([]string{"me@box"}); err == nil || !strings.Contains(err.Error(), "without -m") {
		t.Errorf("-m: %v", err)
	}
}

func TestSSHPaneName(t *testing.T) {
	for in, want := range map[string]string{"me@box": "me@box", "ssh://me@box:2222": "me@box:2222", "ssh://me@box/": "me@box", "ssh://": "ssh://"} {
		if got := sshPaneName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
