package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/remote"
)

func machineIDs(m *Model) string {
	var ids []string
	for _, mach := range m.machines {
		ids = append(ids, mach.id+"="+mach.label+"@"+mach.target)
	}
	return strings.Join(ids, ",")
}

// touchCatalog makes sure the next write gets a different stamp even on
// file systems with coarse modification times.
func bumpCatalog(t *testing.T) {
	t.Helper()
	later := time.Now().Add(time.Duration(time.Now().UnixNano()%1000+1) * time.Millisecond)
	os.Chtimes(remote.CatalogPath(), later, later)
}

func TestCatalogFollowsConchMachine(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.catalogStamp = catalogStamp()
	if cmd := m.syncCatalog(); cmd != nil {
		t.Fatal("no file, no change: expected nothing")
	}

	// conch machine add, from another process.
	if _, err := remote.SaveMachine(remote.Machine{Label: "devbox", Target: "dev@10.0.0.115"}); err != nil {
		t.Fatal(err)
	}
	if cmd := m.syncCatalog(); cmd == nil {
		t.Fatal("added machine: no command (connect)")
	}
	if got := machineIDs(m); got != "local=local@,devbox=devbox@dev@10.0.0.115" {
		t.Fatalf("after add: %s", got)
	}
	if m.flash != "added devbox" || !m.expanded[machineID("devbox")] {
		t.Fatalf("flash %q expanded %v", m.flash, m.expanded[machineID("devbox")])
	}
	// Unchanged file: nothing to do, the machine object is kept.
	box := m.machines[1]
	if m.syncCatalog() != nil || m.machines[1] != box {
		t.Fatal("unchanged file changed machines")
	}

	// A second machine, then a relabel of the first by hand.
	remote.SaveMachine(remote.Machine{Label: "gpu", Target: "ada@gpu"})
	bumpCatalog(t)
	m.syncCatalog()
	remote.SaveMachine(remote.Machine{Label: "build box", Target: "dev@10.0.0.115"})
	bumpCatalog(t)
	m.flash = ""
	m.syncCatalog()
	if got := machineIDs(m); got != "local=local@,devbox=build box@dev@10.0.0.115,gpu=gpu@ada@gpu" || m.machines[1] != box || m.flash != "" {
		t.Fatalf("relabel: %s (same object %v) flash %q", got, m.machines[1] == box, m.flash)
	}

	// A changed target reconnects with a new machine.
	b, _ := os.ReadFile(remote.CatalogPath())
	os.WriteFile(remote.CatalogPath(), []byte(strings.Replace(string(b), "dev@10.0.0.115", "dev@10.0.0.116", 1)), 0o600)
	bumpCatalog(t)
	if cmd := m.syncCatalog(); cmd == nil || m.machines[1] == box || m.machines[1].target != "dev@10.0.0.116" {
		t.Fatalf("target change: %s", machineIDs(m))
	}

	// A broken file is ignored until it is fixed.
	good, _ := os.ReadFile(remote.CatalogPath())
	os.WriteFile(remote.CatalogPath(), []byte("{not json"), 0o600)
	bumpCatalog(t)
	before := machineIDs(m)
	if m.syncCatalog() != nil || machineIDs(m) != before {
		t.Fatal("broken file changed machines")
	}
	os.WriteFile(remote.CatalogPath(), good, 0o600)
	bumpCatalog(t)
	m.syncCatalog()
	if machineIDs(m) != before {
		t.Fatalf("fixed file: %s", machineIDs(m))
	}

	// conch machine rm; the viewed pane of a removed machine is let go.
	m.viewMachine, m.viewing = "gpu", "p1"
	if err := remote.RemoveMachine("gpu"); err != nil {
		t.Fatal(err)
	}
	bumpCatalog(t)
	m.syncCatalog()
	if got := machineIDs(m); got != "local=local@,devbox=build box@dev@10.0.0.116" || m.flash != "removed gpu" || m.viewMachine != "" {
		t.Fatalf("after rm: %s flash %q viewing %q", got, m.flash, m.viewMachine)
	}

	// A disabled machine is removed too; an entry named local never replaces this computer.
	b, _ = os.ReadFile(remote.CatalogPath())
	s := strings.Replace(string(b), `"enabled": true`, `"enabled": false`, 1)
	s = strings.Replace(s, `"machines": [`, `"machines": [{"id": "local", "label": "impostor", "target": "x@y", "enabled": true},`, 1)
	os.WriteFile(remote.CatalogPath(), []byte(s), 0o600)
	bumpCatalog(t)
	m.syncCatalog()
	if got := machineIDs(m); got != "local=local@" {
		t.Fatalf("disabled and local entries: %s", got)
	}
}

// Changes the TUI makes itself (adding or removing in the tree) leave
// nothing for the watcher to do.
func TestCatalogOwnChangesAreNoOps(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	saved, _ := remote.SaveMachine(remote.Machine{Label: "devbox", Target: "dev@box"})
	next, _ := m.update(machineAddedMsg{m: saved})
	*m = next.(Model)
	box := m.machines[len(m.machines)-1]
	m.syncCatalog()
	if len(m.machines) != 2 || m.machines[1] != box {
		t.Fatalf("own add: %s", machineIDs(m))
	}
	m.removeMachine("devbox")
	m.syncCatalog()
	if machineIDs(m) != "local=local@" {
		t.Fatalf("own remove: %s", machineIDs(m))
	}
}
