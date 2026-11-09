package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
)

// A pane named machine:pN — as conch's windows copy one — reaches that
// machine, as -m does; anything that merely looks like one is left alone.
func TestMachinePaneRef(t *testing.T) {
	a4Env(t)
	bb, err := remote.SaveMachine(remote.Machine{ID: "10-0-0-115", Label: "busybox", Target: "aghadge@10.0.0.115", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cmd, machine     string
		args, want       []string
		wantMachine, bad string
	}{
		{cmd: "read", args: []string{"busybox:p3"}, want: []string{"p3"}, wantMachine: bb.ID},
		{cmd: "read", args: []string{bb.ID + ":p3"}, want: []string{"p3"}, wantMachine: bb.ID},
		{cmd: "read", args: []string{"aghadge@10.0.0.115:p3"}, want: []string{"p3"}, wantMachine: bb.ID}, // its ssh target, as -m takes
		{cmd: "send", args: []string{"-keys", "busybox:p12", "Enter"}, want: []string{"-keys", "p12", "Enter"}, wantMachine: bb.ID},
		{cmd: "wait", args: []string{"local:p4"}, want: []string{"p4"}, wantMachine: "local"},
		{cmd: "agent", args: []string{"answer", "busybox:p2", "1"}, want: []string{"answer", "p2", "1"}, wantMachine: bb.ID},
		// -m and the ref agree, by ID or by label.
		{cmd: "read", machine: "busybox", args: []string{"busybox:p3"}, want: []string{"p3"}, wantMachine: bb.ID},
		{cmd: "read", machine: bb.ID, args: []string{"busybox:p3"}, want: []string{"p3"}, wantMachine: bb.ID},
		// Only the pane; text after it that looks like one stays text.
		{cmd: "send", args: []string{"busybox:p3", "busybox:p4"}, want: []string{"p3", "busybox:p4"}, wantMachine: bb.ID},
		{cmd: "send", args: []string{"p5", "look", "at", "busybox:p3"}, want: []string{"p5", "look", "at", "busybox:p3"}},
		{cmd: "send", args: []string{"-keys", "p5", "busybox:p3"}, want: []string{"-keys", "p5", "busybox:p3"}},
		{cmd: "send", args: []string{"p5", "--", "busybox:p3"}, want: []string{"p5", "--", "busybox:p3"}},
		{cmd: "agent", args: []string{"prompt", "p5", "busybox:p3"}, want: []string{"prompt", "p5", "busybox:p3"}},
		{cmd: "rename", args: []string{"p5", "busybox:p3"}, want: []string{"p5", "busybox:p3"}},
		// After --, the flag package's next argument is the pane.
		{cmd: "send", args: []string{"--", "busybox:p3", "hi"}, want: []string{"--", "p3", "hi"}, wantMachine: bb.ID},
		// Flags that take a value, before the pane: the value is not it.
		{cmd: "wait", args: []string{"-state", "done", "-timeout", "5m", "busybox:p3"}, want: []string{"-state", "done", "-timeout", "5m", "p3"}, wantMachine: bb.ID},
		{cmd: "wait", args: []string{"-state=done", "--timeout", "5m", "busybox:p3"}, want: []string{"-state=done", "--timeout", "5m", "p3"}, wantMachine: bb.ID},
		{cmd: "agent", args: []string{"prompt", "-wait", "-until", "done", "-at", "+30m", "busybox:p3", "fix", "busybox:p4"}, want: []string{"prompt", "-wait", "-until", "done", "-at", "+30m", "p3", "fix", "busybox:p4"}, wantMachine: bb.ID},
		{cmd: "agent", args: []string{"answer", "-question", "q1", "busybox:p3", "1"}, want: []string{"answer", "-question", "q1", "p3", "1"}, wantMachine: bb.ID},
		{cmd: "agent", args: []string{"explain", "busybox:p3"}, want: []string{"explain", "p3"}, wantMachine: bb.ID},
		{cmd: "agent", args: []string{"question", "busybox:p3"}, want: []string{"question", "p3"}, wantMachine: bb.ID},
		{cmd: "attach", args: []string{"-raw", "-view", "busybox:p3"}, want: []string{"-raw", "-view", "p3"}, wantMachine: bb.ID},
		{cmd: "show", args: []string{"-name", "pair", "busybox:p3", "busybox:p4"}, want: []string{"-name", "pair", "p3", "p4"}, wantMachine: bb.ID},
		{cmd: "upload", args: []string{"-to", "~/x", "-ssh", "busybox:p3", "notes:p1"}, want: []string{"-to", "~/x", "-ssh", "p3", "notes:p1"}, wantMachine: bb.ID},
		{cmd: "upload", args: []string{"busybox:p3"}, want: []string{"busybox:p3"}}, // a file's name
		// A flag's value is never the pane, even shaped like one.
		{cmd: "show", args: []string{"-name", "busybox:p3", "p4"}, want: []string{"-name", "busybox:p3", "p4"}},
		{cmd: "agent", args: []string{"answer", "-question", "busybox:p3", "p4", "1"}, want: []string{"answer", "-question", "busybox:p3", "p4", "1"}},
		// Left alone: a plain ID, a machine conch doesn't have, a command
		// or subcommand that takes no pane, not quite a ref, no arguments.
		{cmd: "read", args: []string{"p3"}, want: []string{"p3"}},
		{cmd: "read", args: []string{"nosuch:p3"}, want: []string{"nosuch:p3"}},
		{cmd: "read", args: []string{}, want: []string{}},
		{cmd: "send", args: []string{"-keys"}, want: []string{"-keys"}},
		{cmd: "agent", args: []string{"install", "busybox:p3"}, want: []string{"install", "busybox:p3"}},
		{cmd: "agent", args: []string{}, want: []string{}},
		{cmd: "view", args: []string{"busybox:p3"}, want: []string{"busybox:p3"}},
		{cmd: "status", args: []string{"busybox:p3"}, want: []string{"busybox:p3"}},
		{cmd: "read", args: []string{"busybox:px"}, want: []string{"busybox:px"}},
		{cmd: "read", args: []string{"busybox:p3x"}, want: []string{"busybox:p3x"}},
		// Disagreeing with -m is said, not guessed at.
		{cmd: "read", machine: "local", args: []string{"busybox:p3"}, bad: "and -m says local"},
		{cmd: "read", machine: "busybox", args: []string{"local:p3"}, bad: "and -m says busybox"},
		{cmd: "show", args: []string{"busybox:p3", "local:p4"}, bad: "and -m says " + bb.ID},
	} {
		got, m, err := machinePane(c.cmd, c.args, c.machine)
		if c.bad != "" {
			if err == nil || !strings.Contains(err.Error(), c.bad) {
				t.Errorf("%s %v -m %q: %v, want %q", c.cmd, c.args, c.machine, err, c.bad)
			}
			continue
		}
		want := c.wantMachine
		if want == "" {
			want = c.machine
		}
		if err != nil || !reflect.DeepEqual(got, c.want) || m != want {
			t.Errorf("%s %v -m %q: %v -m %q %v; want %v -m %q", c.cmd, c.args, c.machine, got, m, err, c.want, want)
		}
	}
	// The caller's slice is not changed.
	in := []string{"busybox:p3"}
	machinePane("read", in, "")
	if in[0] != "busybox:p3" {
		t.Error("machinePane changed its argument")
	}
}
