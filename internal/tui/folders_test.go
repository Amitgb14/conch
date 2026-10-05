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

// TestFolderClaimsTheRightPanes: which panes a folder holds, where the id
// and the name disagree.
//
// Two of these came from use. A member matched by id *or* name put a
// folder's own pane and whatever later took that pane's id both in the
// folder, after a restart handed the id out again. Matching by name alone
// then swallowed every pane sharing a name — and sharing one is the point:
// vm1 in eng and vm1 in prod are different machines.
func TestFolderClaimsTheRightPanes(t *testing.T) {
	claim := func(f savedFolder, panes ...proto.PaneInfo) []string {
		var ids []string
		for _, p := range f.claim(panes, map[string]bool{}) {
			ids = append(ids, p.ID)
		}
		return ids
	}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	eng := savedFolder{Name: "eng", Members: []savedMember{{Name: "vm1", ID: "p1"}}}

	// Two panes of one name: the id says which, and the other is untouched.
	if got := claim(eng, proto.PaneInfo{ID: "p1", Name: "vm1"}, proto.PaneInfo{ID: "p7", Name: "vm1"}); !eq(got, "p1") {
		t.Fatalf("with two vm1 it holds %v, want just p1", got)
	}

	// The id gone: nothing is claimed by name here. Finding a pane again
	// by name is settleFolders' job, once per connection — a claim that
	// searched on every rebuild would take a pane nobody gave it
	// (TestFolderDoesNotTakeAPaneThatOutlivedItsNamesake).
	if got := claim(eng, proto.PaneInfo{ID: "p9", Name: "vm1"}); len(got) != 0 {
		t.Fatalf("it found %v by name, which only settling may do", got)
	}

	// An id handed out again to another pane is not this pane.
	if got := claim(eng, proto.PaneInfo{ID: "p1", Name: "spacer"}); len(got) != 0 {
		t.Fatalf("a recycled id gave it %v", got)
	}

	// A pane that never had a name is held by its id, and only until the
	// server that gave it goes.
	none := savedFolder{Name: "eng", Members: []savedMember{{ID: "p7"}}}
	if got := claim(none, proto.PaneInfo{ID: "p7"}); !eq(got, "p7") {
		t.Fatalf("a nameless pane: %v", got)
	}
	if got := claim(none, proto.PaneInfo{ID: "p8", Name: "vm1"}); len(got) != 0 {
		t.Fatalf("its id gone, it holds %v", got)
	}
	_ = eq

	// A pane already claimed by the folder before it is left alone.
	taken := map[string]bool{"p1": true}
	if got := eng.claim([]proto.PaneInfo{{ID: "p1", Name: "vm1"}}, taken); len(got) != 0 {
		t.Fatalf("took a pane another folder has: %v", got)
	}
}

// TestA1FolderTakesTwoPanesOfOneName: putting one pane in a folder used to
// take another of the same name out of it — found in use, with two zsh —
// because the member to delete was chosen by name. A folder of vm1 and vm1
// is the point of folders, so it has to hold both.
func TestA1FolderTakesTwoPanesOfOneName(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	var pid string
	for _, p := range mach.panes {
		if p.ProjectID != "" && m.paneSection(localMachine, p.ID) == kindTerminals {
			pid = p.ProjectID
			break
		}
	}
	if pid == "" {
		t.Fatal("no project terminal in the fixture")
	}
	a := proto.PaneInfo{ID: "pa", Name: "zsh", ProjectID: pid, State: proto.PaneRunning}
	b := proto.PaneInfo{ID: "pb", Name: "zsh", ProjectID: pid, State: proto.PaneRunning}
	mach.panes = append(mach.panes, a, b)

	if !m.newFolder(localMachine, pid, kindTerminals, "eng") {
		t.Fatal("no folder")
	}
	if !m.putInFolder(localMachine, pid, kindTerminals, "eng", a) {
		t.Fatal("the first did not go in")
	}
	if !m.putInFolder(localMachine, pid, kindTerminals, "eng", b) {
		t.Fatal("the second did not go in")
	}
	fs := m.foldersIn(localMachine, pid, kindTerminals)
	if len(fs) != 1 || len(fs[0].Members) != 2 {
		t.Fatalf("the folder holds %+v, want both", fs[0].Members)
	}

	// And the tree lists both, each by its own id.
	held := fs[0].claim(m.sectionPanes(localMachine, pid, kindTerminals), map[string]bool{})
	var ids []string
	for _, p := range held {
		ids = append(ids, p.ID)
	}
	if len(ids) != 2 || ids[0] != "pa" || ids[1] != "pb" {
		t.Fatalf("the tree lists %v, want pa and pb", ids)
	}

	// Taking one out leaves the other, though they share a name.
	if !m.takeOutOfFolders(localMachine, pid, kindTerminals, a) {
		t.Fatal("the first did not come out")
	}
	fs = m.foldersIn(localMachine, pid, kindTerminals)
	if len(fs[0].Members) != 1 || fs[0].Members[0].ID != "pb" {
		t.Fatalf("after taking pa out: %+v, want pb alone", fs[0].Members)
	}

	// A pane that is not a pane goes in no folder.
	if m.putInFolder(localMachine, pid, kindTerminals, "eng", proto.PaneInfo{}) {
		t.Fatal("a pane with no id went in")
	}
	if len(m.foldersIn(localMachine, pid, kindTerminals)[0].Members) != 1 {
		t.Fatal("it left something behind")
	}
}

// TestFolderDoesNotTakeAPaneThatOutlivedItsNamesake: the bug from a real
// tree. A folder eng in Terminals had a member written down as "zsh" whose
// id was from an older server, and two terminals called zsh were running
// loose. The folder held neither, the name being ambiguous — and then one
// of them was closed, the name stopped being ambiguous, and the survivor
// walked into a folder nobody had put it in.
func TestFolderDoesNotTakeAPaneThatOutlivedItsNamesake(t *testing.T) {
	m := &Model{folders: map[string][]savedFolder{
		"r1/4": {{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p77"}}}},
	}}
	two := []proto.PaneInfo{{ID: "p1", Name: "zsh"}, {ID: "p2", Name: "zsh"}}

	// Settling with two of that name and no live id lets the member go:
	// there is nothing to choose by, and keeping it leaves the trap.
	if !m.settleFolders(localMachine, two) {
		t.Fatal("settling changed nothing")
	}
	if mem := m.folders["r1/4"][0].Members; len(mem) != 0 {
		t.Fatalf("it kept %+v", mem)
	}

	// With the member gone, closing one zsh leaves the other where it was.
	for _, panes := range [][]proto.PaneInfo{two, {two[1]}} {
		if got := m.folders["r1/4"][0].claim(panes, map[string]bool{}); len(got) != 0 {
			t.Fatalf("with %d panes the folder holds %+v", len(panes), got)
		}
	}
}

// TestSettleFindsPanesAgainAfterARestart: the reason the name is written
// down at all. A server that restarts hands out new ids, and the folder
// finds its pane again by name — once, and the id it adopts is what holds
// it from then on.
func TestSettleFindsPanesAgainAfterARestart(t *testing.T) {
	m := &Model{folders: map[string][]savedFolder{
		"r1/4": {{Name: "eng", Members: []savedMember{{Name: "vm1", ID: "old-p3"}, {Name: "vm2", ID: "old-p4"}}}},
	}}
	// One of them is running under a new id; the other is not there.
	if !m.settleFolders(localMachine, []proto.PaneInfo{{ID: "p9", Name: "vm1"}, {ID: "p8", Name: "spare"}}) {
		t.Fatal("settling changed nothing")
	}
	mem := m.folders["r1/4"][0].Members
	if len(mem) != 1 || mem[0].ID != "p9" || mem[0].Name != "vm1" {
		t.Fatalf("members %+v", mem)
	}
	// And from then on it is held by that id, through renames and all.
	got := m.folders["r1/4"][0].claim([]proto.PaneInfo{{ID: "p9", Name: "vm1"}}, map[string]bool{})
	if len(got) != 1 || got[0].ID != "p9" {
		t.Fatalf("it holds %+v", got)
	}
	// Settling again with the pane still there changes nothing: a reload
	// keeps the ids, so there is nothing to find.
	if m.settleFolders(localMachine, []proto.PaneInfo{{ID: "p9", Name: "vm1"}}) {
		t.Error("settling an unchanged machine rewrote the folder")
	}
}

// TestSettleLeavesOtherMachinesAlone: folders are keyed by machine, and a
// machine's panes say nothing about another's.
func TestSettleLeavesOtherMachinesAlone(t *testing.T) {
	m := &Model{folders: map[string][]savedFolder{
		"r1/4":         {{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p1"}}}},
		"busybox~r1/4": {{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p1"}}}},
	}}
	// This computer's panes: the local folder settles, busybox's is left.
	m.settleFolders(localMachine, []proto.PaneInfo{{ID: "p5", Name: "zsh"}})
	if mem := m.folders["r1/4"][0].Members; len(mem) != 1 || mem[0].ID != "p5" {
		t.Fatalf("local %+v", mem)
	}
	if mem := m.folders["busybox~r1/4"][0].Members; len(mem) != 1 || mem[0].ID != "p1" {
		t.Fatalf("busybox was settled by this computer's panes: %+v", mem)
	}
	if got := folderKeyMachine("r1/4"); got != localMachine {
		t.Errorf("a local key reads as machine %q", got)
	}
	if got := folderKeyMachine("busybox~r1/4"); got != "busybox" {
		t.Errorf("a remote key reads as machine %q", got)
	}
}

// TestSettleKeepsTwoPanesOfOneName: a folder that really does hold two
// panes called zsh keeps both, each by its own id.
func TestSettleKeepsTwoPanesOfOneName(t *testing.T) {
	m := &Model{folders: map[string][]savedFolder{
		"r1/4": {{Name: "eng", Members: []savedMember{{Name: "zsh", ID: "p1"}, {Name: "zsh", ID: "p2"}}}},
	}}
	panes := []proto.PaneInfo{{ID: "p1", Name: "zsh"}, {ID: "p2", Name: "zsh"}, {ID: "p3", Name: "zsh"}}
	if m.settleFolders(localMachine, panes) {
		t.Fatal("both are live: there was nothing to settle")
	}
	got := m.folders["r1/4"][0].claim(panes, map[string]bool{})
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p2" {
		t.Fatalf("it holds %+v, and p3 is nobody's", got)
	}
}
