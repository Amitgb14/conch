package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// -copy-key starts the TUI's K terminal: the key copy script, logged in to
// the host as a session would be, named for what it does.
func TestSSHCopyKeyStartsAKeyCopyPane(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var mu sync.Mutex
	var got proto.PaneCreateParams
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneCreate {
			mu.Lock()
			json.Unmarshal(msg.Params, &got)
			mu.Unlock()
			return proto.PaneInfo{ID: "p4", Name: got.Name}, nil
		}
		return nil, nil
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runSSHLogin([]string{"-copy-key", "me@build-box", "-p", "2222"}) })
	if err != nil || strings.TrimSpace(out) != "p4" {
		t.Fatalf("%v %q", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	n := len(got.Command)
	if n < 6 || got.Command[0] != "/bin/sh" || got.Command[n-1] != "me@build-box" || !strings.Contains(strings.Join(got.Command, " "), "-p 2222") {
		t.Fatalf("command %q", got.Command)
	}
	if !strings.Contains(got.Command[2], "authorized_keys") {
		t.Fatalf("not the key copy: %q", got.Command[2])
	}
	if got.Name != "copy key to me@build-box" || !got.NoProject {
		t.Fatalf("params %+v", got)
	}
}

// fakeSSH puts a stand-in ssh on CONCH_SSH that logs its words to a file
// and then runs body.
func fakeSSH(t *testing.T, body string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "args")
	script := filepath.Join(dir, "ssh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+log+"\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SSH", script)
	return log
}

// -check-key logs in in batch mode, its own options before the person's
// (ssh keeps the first), runs true there, and says what came of it as a
// word: key, password, or unreachable with what ssh said.
func TestSSHCheckKey(t *testing.T) {
	a4Env(t)
	for _, c := range []struct{ body, want string }{
		{"exit 0", "key"},
		{"echo 'me@box: Permission denied (publickey,password).' >&2; exit 255", "password me@box: Permission denied (publickey,password)."},
		{"echo 'Received disconnect: Too many authentication failures' >&2; exit 255", "password Received disconnect: Too many authentication failures"},
		{"echo 'ssh: connect to host box port 22: Connection refused' >&2; exit 255", "unreachable ssh: connect to host box port 22: Connection refused"},
		{"echo 'Host key verification failed.' >&2; exit 255", "unreachable Host key verification failed."},
		{"exit 255", "unreachable"},
	} {
		log := fakeSSH(t, c.body)
		var err error
		out, _ := a4Capture(t, "", func() { err = runSSHLogin([]string{"-check-key", "me@box", "-o", "BatchMode=no"}) })
		if err != nil || strings.TrimSpace(out) != c.want {
			t.Errorf("%q: %v %q, want %q", c.body, err, out, c.want)
		}
		b, _ := os.ReadFile(log)
		words := strings.Fields(string(b))
		joined := strings.Join(words, " ")
		if len(words) < 3 || words[len(words)-1] != "true" || words[len(words)-2] != "me@box" {
			t.Fatalf("args %q", words)
		}
		if i, j := strings.Index(joined, "BatchMode=yes"), strings.Index(joined, "BatchMode=no"); i < 0 || j < 0 || i > j {
			t.Fatalf("its own batch mode is not first: %q", joined)
		}
	}
}

// A host that never answers is unreachable once the wait is up, rather
// than holding whatever asked.
func TestSSHCheckKeyGivesUp(t *testing.T) {
	a4Env(t)
	fakeSSH(t, "exec sleep 30")
	old := checkKeyTimeout
	checkKeyTimeout = 200 * time.Millisecond
	defer func() { checkKeyTimeout = old }()
	start := time.Now()
	var err error
	out, _ := a4Capture(t, "", func() { err = runSSHLogin([]string{"-check-key", "me@box"}) })
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "unreachable") {
		t.Fatalf("%v %q", err, out)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("waited %v", time.Since(start))
	}
}

// Both take a target, and options ssh's login takes, on this computer.
func TestSSHKeyModesRefuse(t *testing.T) {
	a4Env(t)
	for _, mode := range []string{"-copy-key", "-check-key"} {
		for _, args := range [][]string{{mode}, {mode, "-p", "22"}} {
			if err := runSSHLogin(args); err == nil || !strings.Contains(err.Error(), sshUsage) {
				t.Errorf("%q: %v", args, err)
			}
		}
		if err := runSSHLogin([]string{mode, "me@box", "-F", "/tmp/x"}); err == nil {
			t.Errorf("%s: an -F was taken", mode)
		}
		machineFlag = "busybox"
		err := runSSHLogin([]string{mode, "me@box"})
		machineFlag = ""
		if err == nil || !strings.Contains(err.Error(), "without -m") {
			t.Errorf("%s -m: %v", mode, err)
		}
	}
}
