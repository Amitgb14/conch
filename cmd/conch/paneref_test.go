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
		// Only the first; text after it that looks like one stays text.
		{cmd: "send", args: []string{"busybox:p3", "busybox:p4"}, want: []string{"p3", "busybox:p4"}, wantMachine: bb.ID},
		// Left alone: a plain ID, a machine conch doesn't have, after --,
		// a command that takes no pane, not quite a ref.
		{cmd: "read", args: []string{"p3"}, want: []string{"p3"}},
		{cmd: "read", args: []string{"nosuch:p3"}, want: []string{"nosuch:p3"}},
		{cmd: "send", args: []string{"--", "busybox:p3"}, want: []string{"--", "busybox:p3"}},
		{cmd: "status", args: []string{"busybox:p3"}, want: []string{"busybox:p3"}},
		{cmd: "read", args: []string{"busybox:px"}, want: []string{"busybox:px"}},
		{cmd: "read", args: []string{"busybox:p3x"}, want: []string{"busybox:p3x"}},
		// Disagreeing with -m is said, not guessed at.
		{cmd: "read", machine: "local", args: []string{"busybox:p3"}, bad: "and -m says local"},
		{cmd: "read", machine: "busybox", args: []string{"local:p3"}, bad: "and -m says busybox"},
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
