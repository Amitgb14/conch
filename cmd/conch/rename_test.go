package main

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
)

func TestMachineRenameCommand(t *testing.T) {
	a4Env(t)
	saved, _ := remote.SaveMachine(remote.Machine{Target: "dev@box"})
	var err error
	out, _ := a4Capture(t, "", func() { err = runMachine([]string{"rename", saved.ID, "devbox"}) })
	if err != nil || out != "renamed "+saved.ID+" to devbox\n" {
		t.Fatalf("rename: %q %v", out, err)
	}
	if ms, _ := remote.Machines(); ms[0].Label != "devbox" {
		t.Fatalf("saved: %+v", ms)
	}
	for _, args := range [][]string{{"rename"}, {"rename", "devbox"}, {"rename", "a", "b", "c"}} {
		if err := runMachine(args); err == nil || !strings.Contains(err.Error(), "usage: conch machine rename") {
			t.Errorf("%q: %v", args, err)
		}
	}
	if err := runMachine([]string{"rename", "nope", "x"}); err == nil || !strings.Contains(err.Error(), `no machine "nope"`) {
		t.Fatalf("unknown: %v", err)
	}
}
