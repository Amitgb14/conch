package remote

import (
	"strings"
	"testing"
)

// An ID reaches its own machine, whatever another machine is labelled or
// targeted: a label or target only counts when no ID is that name.
func TestLookupMachinePrefersID(t *testing.T) {
	a4Env(t)
	// A catalog an older build could write: labels that clash with IDs.
	if err := saveMachines([]Machine{
		{ID: "a", Label: "b", Target: "c", Enabled: true},
		{ID: "b", Label: "x", Target: "a", Enabled: true},
		{ID: "c", Label: "y", Target: "z", Enabled: true},
		{ID: "d", Label: "same", Target: "d1", Enabled: true},
		{ID: "e", Label: "same", Target: "e1", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{
		"a": "a", "b": "b", "c": "c", // by ID, before a label or target
		"x": "b", "y": "c", // by label
		"z": "c", "d1": "d", // by target
		"same": "d", // a shared label: the first, as before
	} {
		if m, ok := LookupMachine(ref); !ok || m.ID != want {
			t.Errorf("LookupMachine(%q) = %q %v, want %q", ref, m.ID, ok, want)
		}
	}
	if _, ok := LookupMachine("nosuch"); ok {
		t.Error("found a machine conch doesn't have")
	}
	if _, ok := LookupMachine(""); ok {
		t.Error("found a machine by an empty name")
	}
}

// What a window puts before a pane's ID has to reach that machine again.
func TestRefName(t *testing.T) {
	ms := []Machine{
		{ID: "devbox", Label: "devbox"},
		{ID: "devbox-2", Label: "devbox"}, // a shared label: neither may use it
		{ID: "bb", Label: "busybox"},
		{ID: "lab", Label: "lab box"},
		{ID: "tab", Label: "lab\tbox"},
		{ID: "nl", Label: "lab\nbox"},
		{ID: "odd", Label: "a:b"},
		{ID: "loc", Label: "local"},
		{ID: "x", Label: "bb"}, // another machine's ID
		{ID: "none", Label: ""},
		{ID: "off", Label: "spare", Enabled: false}, // disabled still counts
		{ID: "on", Label: "spare", Enabled: true},
	}
	for id, want := range map[string]string{
		"devbox": "devbox", "devbox-2": "devbox-2",
		"bb": "busybox", "lab": "lab", "tab": "tab", "nl": "nl", "odd": "odd",
		"loc": "loc", "x": "x", "none": "none", "off": "off", "on": "on",
		"gone": "gone", // not in the list at all
	} {
		got := RefName(ms, id)
		if got != want {
			t.Errorf("RefName(%q) = %q, want %q", id, got, want)
		}
		// And it resolves back: the point of it.
		if m, ok := lookupIn(ms, got); id != "gone" && (!ok || m.ID != id) {
			t.Errorf("RefName(%q) = %q resolves to %q", id, got, m.ID)
		}
	}
	if got := RefName(nil, "bb"); got != "bb" {
		t.Errorf("no machines: %q", got)
	}
	if got := RefName([]Machine{{ID: "bb", Label: "busybox"}}, "bb"); got != "busybox" {
		t.Errorf("a list of one: %q", got)
	}
}

// Two logins to one host default to the same label; the second is given
// its ID instead, so each can be named. A label that would be "local" or
// another machine's ID is not given either.
func TestSaveMachineKeepsLabelsDistinct(t *testing.T) {
	a4Env(t)
	a, err := SaveMachine(Machine{Target: "alice@devbox"})
	if err != nil || a.ID != "devbox" || a.Label != "devbox" {
		t.Fatalf("first: %+v %v", a, err)
	}
	b, err := SaveMachine(Machine{Target: "bob@devbox"})
	if err != nil || b.ID != "devbox-2" || b.Label != "devbox-2" {
		t.Fatalf("second: %+v %v", b, err)
	}
	c, err := SaveMachine(Machine{Target: "carol@gpu", Label: "devbox"}) // given, and taken
	if err != nil || c.Label != c.ID || c.ID != "devbox-3" {
		t.Fatalf("given a taken label: %+v %v", c, err)
	}
	d, err := SaveMachine(Machine{Target: "dan@x", Label: "local"})
	if err != nil || d.Label == "local" || d.Label != d.ID {
		t.Fatalf("labelled local: %+v %v", d, err)
	}
	if _, err := RenameMachine(a.ID, "gpu"); err != nil {
		t.Fatal(err)
	}
	// An ID is not handed out when it is another machine's label.
	e, err := SaveMachine(Machine{Target: "eve@gpu"})
	if err != nil || e.ID == "gpu" || e.Label != e.ID {
		t.Fatalf("an ID that is a label: %+v %v", e, err)
	}
	// Saved again with a label another machine has: kept as it was.
	again, err := SaveMachine(Machine{Target: "bob@devbox", Label: "gpu"})
	if err != nil || again.ID != b.ID || again.Label != b.Label {
		t.Fatalf("relabelled to a taken label: %+v %v", again, err)
	}
	// And with one nobody has: renamed, as before.
	if again, err := SaveMachine(Machine{Target: "bob@devbox", Label: "bobs"}); err != nil || again.Label != "bobs" {
		t.Fatalf("relabelled: %+v %v", again, err)
	}
	ms, _ := Machines()
	for _, m := range ms {
		if got := RefName(ms, m.ID); got != m.Label {
			t.Errorf("%s: named %q, labelled %q", m.ID, got, m.Label)
		}
	}
}

func TestRenameMachineRefusesIDsAndLocal(t *testing.T) {
	a4Env(t)
	a, _ := SaveMachine(Machine{Target: "dev@one"})
	b, _ := SaveMachine(Machine{Target: "dev@two"})
	if _, err := RenameMachine(a.ID, b.ID); err == nil || !strings.Contains(err.Error(), "already called") {
		t.Errorf("another machine's ID: %v", err)
	}
	if _, err := RenameMachine(a.ID, "local"); err == nil || !strings.Contains(err.Error(), "this computer") {
		t.Errorf("local: %v", err)
	}
	if m, err := RenameMachine(a.ID, a.ID); err != nil || m.Label != a.ID { // its own ID is fine
		t.Errorf("its own ID: %+v %v", m, err)
	}
}
