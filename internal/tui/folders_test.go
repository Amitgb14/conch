package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestA1FoldersInTheTree: making a folder, dragging a pane into it and back
// out, and removing it — the panes going back to the section, never closing.
func TestA1FoldersInTheTree(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.focus = focusSidebar

	// A pane to put somewhere, and the section it is listed under.
	var paneRow row
	for _, r := range m.rows {
		if r.kind == kindPane {
			paneRow = r
			break
		}
	}
	if paneRow.id == "" {
		t.Fatalf("no pane in\n%s", render(m.rows))
	}
	mid, pid, kind, ok := m.folderSection(paneRow)
	if !ok {
		t.Fatal("the pane is in no section")
	}

	// Made, and listed with nothing in it.
	if !m.newFolder(mid, pid, kind, "eng") {
		t.Fatal("no folder made")
	}
	if m.newFolder(mid, pid, kind, "eng") {
		t.Fatal("a second folder of the same name")
	}
	m.rebuild()
	fid := folderRowID(mid, pid, kind, "eng")
	if !strings.Contains(render(m.rows), fid) {
		t.Fatalf("the folder is not listed:\n%s", render(m.rows))
	}

	// The pane dragged in.
	if cmd := m.dropRowOnTab(paneRow.id, fid); cmd == nil {
		t.Fatal("the pane did not go in")
	}
	m.rebuild()
	if !strings.Contains(render(m.rows), fid+"\n") {
		t.Fatalf("the folder went:\n%s", render(m.rows))
	}
	fs := m.foldersIn(mid, pid, kind)
	if len(fs) != 1 || len(fs[0].Members) != 1 {
		t.Fatalf("what the folder holds: %+v", fs)
	}
	if fs[0].Members[0].ID != paneRow.paneID {
		t.Fatalf("it holds %+v, want %s", fs[0].Members[0], paneRow.paneID)
	}

	// And dragged back out onto its section.
	var secRow row
	for _, r := range m.rows {
		if r.kind == kind && r.machine == mid && r.projectID == pid {
			secRow = r
			break
		}
	}
	if secRow.id == "" {
		t.Fatalf("no %s row in\n%s", sectionWord(kind), render(m.rows))
	}
	if cmd := m.dropRowOnTab(paneRow.id, secRow.id); cmd == nil {
		t.Fatal("the pane did not come out")
	}
	if fs := m.foldersIn(mid, pid, kind); len(fs) != 1 || len(fs[0].Members) != 0 {
		t.Fatalf("after taking it out: %+v", fs)
	}
	// Out of a folder it is not in: nothing to do.
	if m.dropRowOnTab(paneRow.id, secRow.id) != nil {
		t.Fatal("took a pane out of nothing")
	}

	// A folder of another section does not take it.
	other := kindAgents
	if kind == kindAgents {
		other = kindTerminals
	}
	m.newFolder(mid, pid, other, "elsewhere")
	m.rebuild()
	if cmd := m.dropRowOnTab(paneRow.id, folderRowID(mid, pid, other, "elsewhere")); cmd != nil {
		t.Fatal("a folder of another section took the pane")
	}

	// Removed: the folder goes, the pane stays.
	m.dropRowOnTab(paneRow.id, fid)
	if !m.removeFolder(mid, pid, kind, "eng") {
		t.Fatal("the folder was not removed")
	}
	if m.removeFolder(mid, pid, kind, "eng") {
		t.Fatal("removed twice")
	}
	m.rebuild()
	out := render(m.rows)
	if strings.Contains(out, fid) {
		t.Fatalf("the folder stayed:\n%s", out)
	}
	if !strings.Contains(out, paneRow.id) {
		t.Fatalf("the pane went with it:\n%s", out)
	}
	if m.pane(paneRow.machine, paneRow.paneID) == nil {
		t.Fatal("the pane was closed")
	}

	// With no folder anywhere, a press on a row starts no drag.
	m.folders = nil
	m.cfg.UI.TreeGroups = ""
	nm, _ := m.Update(flashExpiredMsg{})
	*m = nm.(Model)
	for i, r := range m.rows {
		if r.kind == kindPane {
			a1Mouse(t, m, 2, i-m.scroll+2, a1Left, a1Press)
			if m.rowDrag != "" {
				t.Fatalf("a drag started with nowhere to drop: %q", m.rowDrag)
			}
			break
		}
	}
}

// TestFolderHoldsByNameNotARecycledID: a member written down with a name is
// found by that name alone. Ids belong to a running server and the next one
// hands them out again, so matching the id as well once put a folder's pane
// and whatever later took its id both in the folder — seen on a real tree
// after a restart, with the folder holding vm2 and an unrelated spacer2.
func TestFolderHoldsByNameNotARecycledID(t *testing.T) {
	f := savedFolder{Name: "eng", Members: []savedMember{{Name: "vm2", ID: "p2"}}}
	if !f.holds(proto.PaneInfo{ID: "p4", Name: "vm2"}) {
		t.Fatal("the pane was not found again by its name")
	}
	if f.holds(proto.PaneInfo{ID: "p2", Name: "spacer2"}) {
		t.Fatal("a pane that took the old id was taken for it")
	}
	if f.holds(proto.PaneInfo{ID: "p2"}) {
		t.Fatal("a nameless pane on the old id was taken for it")
	}
	if f.holds(proto.PaneInfo{ID: "p9", Name: "vm3"}) {
		t.Fatal("another pane entirely")
	}

	// A pane that never had a name is held by id, and only until the server
	// that gave it goes.
	g := savedFolder{Name: "eng", Members: []savedMember{{ID: "p7"}}}
	if !g.holds(proto.PaneInfo{ID: "p7"}) {
		t.Fatal("a nameless pane was not held by its id")
	}
	if !g.holds(proto.PaneInfo{ID: "p7", Name: "named later"}) {
		t.Fatal("naming it threw it out")
	}
	if g.holds(proto.PaneInfo{ID: "p8"}) {
		t.Fatal("another id")
	}
}
