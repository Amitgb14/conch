package remote

import (
	"strings"
	"testing"
)

func TestRenameMachine(t *testing.T) {
	a4Env(t)
	a, _ := SaveMachine(Machine{Target: "dev@10.0.0.115"})
	b, _ := SaveMachine(Machine{Target: "ada@gpu", Label: "gpu"})

	m, err := RenameMachine(a.ID, "  devbox  ")
	if err != nil || m.ID != a.ID || m.Label != "devbox" || m.Target != "dev@10.0.0.115" {
		t.Fatalf("by id: %+v %v", m, err)
	}
	if m, err := RenameMachine("devbox", "build box"); err != nil || m.ID != a.ID { // by label
		t.Fatalf("by label: %+v %v", m, err)
	}
	ms, _ := Machines()
	if len(ms) != 2 || ms[0].Label != "build box" || ms[0].ID != a.ID || !ms[0].Enabled || ms[1].ID != b.ID || ms[1].Label != "gpu" {
		t.Fatalf("saved: %+v", ms)
	}
	for ref, label := range map[string]string{a.ID: "   ", "nope": "x"} {
		if _, err := RenameMachine(ref, label); err == nil {
			t.Errorf("%q → %q: no error", ref, label)
		}
	}
	if _, err := RenameMachine(a.ID, "gpu"); err == nil || !strings.Contains(err.Error(), "already called") {
		t.Fatalf("duplicate label: %v", err)
	}
	if m, err := RenameMachine(b.ID, "gpu"); err != nil || m.Label != "gpu" { // its own label again is fine
		t.Fatalf("same label: %v", err)
	}
}
