package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A gate coming to wait is said once, with what to type; on another
// machine, with -m; and again only if it comes to wait anew.
func TestRunGateIsSaid(t *testing.T) {
	m := Model{}
	mach := &machine{id: localMachine}
	gate := proto.RunTask{RunTaskSpec: proto.RunTaskSpec{ID: "ship", Spec: "x", Gate: "release it?"}, Status: proto.RunBlocked, Why: proto.WhyGate + "release it?"}
	pending := gate
	pending.Status, pending.Why = proto.RunPending, "waiting for a"
	run := func(tasks ...proto.RunTask) proto.Message {
		return proto.Message{Event: proto.EventRunChanged, Data: proto.Marshal(proto.Run{ID: "r1", Name: "job", Tasks: tasks})}
	}

	m.handleEvent(mach, run(pending))
	if m.flash != "" {
		t.Fatalf("a gate not yet reached was said: %q", m.flash)
	}
	m.handleEvent(mach, run(gate))
	if want := "run job: ship waits for your approval — release it? · conch run approve job ship"; m.flash != want {
		t.Fatalf("flash %q, want %q", m.flash, want)
	}
	m.flash = ""
	m.handleEvent(mach, run(gate))
	if m.flash != "" {
		t.Fatalf("said twice: %q", m.flash)
	}
	approved := gate
	approved.Approved, approved.Status, approved.Why = true, proto.RunReady, ""
	m.handleEvent(mach, run(approved))
	m.handleEvent(mach, run(gate))
	if m.flash == "" {
		t.Fatal("a gate waiting anew was not said")
	}

	remote := &machine{id: "busybox"}
	m.handleEvent(remote, run(gate))
	if !strings.Contains(m.flash, "conch -m busybox run approve job ship") {
		t.Fatalf("remote: %q", m.flash)
	}
	// Something that is not a run is ignored.
	m.flash = ""
	m.handleEvent(mach, proto.Message{Event: proto.EventRunChanged, Data: []byte(`"nope"`)})
	if m.flash != "" {
		t.Fatal(m.flash)
	}
}
