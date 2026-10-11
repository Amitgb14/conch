package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
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
		// The job reports its steps as it goes, so what comes back is the
		// batch of both: the reason is in there.
		found := false
		for _, msg := range a2Run(addMachine("dev@box", "box", c.password, c.key, true)) {
			if e, ok := msg.(errMsg); ok && strings.Contains(e.err.Error(), "Permission denied") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%+v: no reason among the messages", c)
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

// Adding a machine says what it is doing too: connecting, installing,
// copying — the same line a sandbox uses, for the same reason.
func TestAddMachineSaysWhatItIsDoing(t *testing.T) {
	a2Isolate(t)
	ch := make(chan string, 16)
	var progress func(string)
	old := connectFn
	connectFn = func(_ context.Context, _ remote.Transport, o remote.Options) (*client.Client, error) {
		progress = o.Progress
		if o.Progress != nil {
			o.Progress("copying conch to gpu (16 MB)…")
		}
		return nil, errors.New("far enough")
	}
	t.Cleanup(func() { connectFn = old })

	a2Run(addMachineWith("dev@gpu", "gpu", "", false, true, ch))
	var said []string
	for cmd := nextStep(ch); cmd != nil; {
		st, ok := cmd().(stepMsg)
		if !ok {
			break
		}
		said = append(said, st.step)
		cmd = nextStep(st.ch)
	}
	if progress == nil {
		t.Fatal("the install was given nothing to report to")
	}
	want := []string{"connecting to gpu…", "copying conch to gpu (16 MB)…"}
	if strings.Join(said, " | ") != strings.Join(want, " | ") {
		t.Fatalf("steps %q, want %q", said, want)
	}
}

// TestAddMachineWithoutInstalling: unticking Install adds the machine and
// writes nothing to it. Declining the install used to throw the add away
// with it, which made it a cancel rather than a choice.
func TestAddMachineWithoutInstalling(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")

	var asked []remote.Options
	old := connectFn
	connectFn = func(_ context.Context, _ remote.Transport, o remote.Options) (*client.Client, error) {
		asked = append(asked, o)
		// What a machine with no conch on it answers when conch was told
		// not to install one.
		return nil, &remote.InstallError{Platform: "linux/amd64", Reason: "conch is not installed on box (linux/amd64)"}
	}
	t.Cleanup(func() { connectFn = old })

	var added *machineAddedMsg
	for _, msg := range a2Run(addMachine("dev@box", "box", "", false, false)) {
		if m, ok := msg.(machineAddedMsg); ok {
			added = &m
		}
		if e, ok := msg.(errMsg); ok {
			t.Fatalf("not added: %v", e.err)
		}
	}
	if added == nil {
		t.Fatal("no machine added")
	}
	if !strings.Contains(added.note, "without conch on it") {
		t.Fatalf("note %q", added.note)
	}
	if len(asked) != 1 || asked[0].Install {
		t.Fatalf("connect options %+v", asked)
	}
	if ms, _ := remote.Machines(); len(ms) != 1 || ms[0].Target != "dev@box" {
		t.Fatalf("catalog %+v", ms)
	}

	// With Install ticked, the same answer is a failure and nothing is
	// saved: conch was asked to install and could not.
	asked = nil
	if ms, _ := remote.Machines(); len(ms) == 1 {
		_ = remote.RemoveMachine(ms[0].ID)
	}
	failed := false
	for _, msg := range a2Run(addMachine("dev@box2", "box2", "", false, true)) {
		if _, ok := msg.(errMsg); ok {
			failed = true
		}
	}
	if !failed {
		t.Fatal("an install that could not happen was not reported")
	}
	if len(asked) != 1 || !asked[0].Install {
		t.Fatalf("connect options %+v", asked)
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatalf("saved after a failed install: %+v", ms)
	}
}
