package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A plain folder's row offers to make it a git repository; the server is
// asked only once the person confirms, and an older server is named as the
// reason rather than asked.
func TestGitInitFromProjectMenu(t *testing.T) {
	m, peer := plainTaskModel(t)
	find := func(r row) *menuItem {
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			if strings.HasPrefix(it.label, "Make it a git repository") {
				return &it
			}
		}
		return nil
	}
	if find(row{kind: kindProject, machine: localMachine, projectID: "r1"}) != nil {
		t.Fatal("a git project offers git init")
	}
	it := find(row{kind: kindProject, machine: localMachine, projectID: "n1"})
	if it == nil {
		t.Fatal("a plain folder's menu has no git init")
	}

	// harvestModel's server lacks project.git_init.v1.
	if it.run(m) != nil || m.overlay != nil || !strings.Contains(m.flash, "reload it") {
		t.Fatalf("old server: overlay %T flash %q", m.overlay, m.flash)
	}

	m, peer = harvestModel(t, harvestCapability, proto.CapProjectGitInit)
	m.machines[0].projects = append(m.machines[0].projects, proto.ProjectInfo{ID: "n1", Name: "notes", Path: "/src/notes"})
	m.rebuild()
	it = find(row{kind: kindProject, machine: localMachine, projectID: "n1"})
	it.run(m)
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("no question: %T", m.overlay)
	}
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodProjectGitInit {
			t.Fatal("asked the server before the person said yes")
		}
	}
	msgs := submitDialog(t, m)
	if ref := lastParams[proto.ProjectRef](t, peer, proto.MethodProjectGitInit); ref.ID != "n1" {
		t.Fatalf("git_init of %q", ref.ID)
	}
	if len(msgs) == 0 || !strings.Contains(string(msgs[len(msgs)-1].(flashMsg)), "is a git repository now") {
		t.Fatalf("said %#v", msgs)
	}

	// Its plain task names the way to branches.
	d := newPlainTaskDialog(*m, localMachine, "notes", "/src/notes", "/src/notes", false)
	if !strings.Contains(strings.Join(d.text, " "), "Make it a git repository") {
		t.Fatalf("plain task intro: %q", d.text)
	}
}
