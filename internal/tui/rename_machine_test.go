package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
)

func TestRenameMachineInTUI(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	saved, _ := remote.SaveMachine(remote.Machine{Target: "dev@10.0.0.115"})
	remote.SaveMachine(remote.Machine{Target: "ada@gpu", Label: "gpu"})
	m.syncCatalog() // picks up both machines
	m.rebuild()

	// The local machine keeps its name.
	m.cursor = machineID(localMachine)
	m.openRename()
	if m.overlay != nil || m.flash != "this computer is always called local" {
		t.Fatalf("local: %q", m.flash)
	}

	// r on a remote machine: a dialog with its label.
	m.cursor = machineID(saved.ID)
	next, _ := m.handleKey(a2Key("r"))
	*m = next.(Model)
	d, ok := m.overlay.(*dialog)
	if !ok || d.fields[0].in.Value() != "10.0.0.115" || !strings.Contains(strings.Join(d.text, " "), "dev@10.0.0.115") {
		t.Fatalf("dialog: %#v", m.overlay)
	}
	// A label another machine has is refused and nothing changes.
	d.submit(m, []string{"gpu"})
	if !strings.Contains(m.flash, "already called") || m.machine(saved.ID).label != "10.0.0.115" {
		t.Fatalf("duplicate: %q", m.flash)
	}
	d.submit(m, []string{"   "})
	if !strings.Contains(m.flash, "needs a label") {
		t.Fatalf("empty: %q", m.flash)
	}
	// Renamed: the tree, the file and the watcher agree.
	if d.submit(m, []string{" devbox "}); m.flash != "renamed to devbox" || m.machine(saved.ID).label != "devbox" {
		t.Fatalf("rename: %q", m.flash)
	}
	if ms, _ := remote.Machines(); ms[0].Label != "devbox" || ms[0].ID != saved.ID {
		t.Fatalf("file: %+v", ms)
	}
	if m.syncCatalog() != nil {
		t.Fatal("the watcher saw our own rename as a change")
	}
	// The row menu offers it for remote machines only.
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindMachine, machine: saved.ID}, 0, 0)); !strings.Contains(got, "r Rename…") {
		t.Fatalf("menu: %s", got)
	}
	// A machine that went away meanwhile: nothing happens.
	m.openRenameMachine("gone")
}
