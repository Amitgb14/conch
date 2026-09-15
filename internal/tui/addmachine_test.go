package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
)

// A failing ssh ends adding a machine with its error, saves nothing, and
// never puts the password on a command line.
func TestAddMachineSSHFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_SSH_CONFIG", "")
	log := filepath.Join(dir, "ssh.log")
	ssh := filepath.Join(dir, "ssh")
	os.WriteFile(ssh, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\necho 'dev@box: Permission denied (publickey,password).' >&2\nexit 255\n"), 0o755)
	t.Setenv("CONCH_SSH", ssh)

	for _, c := range []struct {
		password string
		key      bool
	}{{"", false}, {"s3cret pass", true}, {"s3cret pass", false}} {
		msg := addMachine("dev@box", "box", c.password, c.key)()
		e, ok := msg.(errMsg)
		if !ok || !strings.Contains(e.err.Error(), "Permission denied") {
			t.Fatalf("%+v: %#v", c, msg)
		}
	}
	if b, _ := os.ReadFile(log); len(b) == 0 || strings.Contains(string(b), "s3cret") {
		t.Fatalf("ssh calls:\n%s", b)
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatalf("saved after a failure: %+v", ms)
	}
	if catalogTick() == nil {
		t.Fatal("the catalog watcher ticks")
	}
}
