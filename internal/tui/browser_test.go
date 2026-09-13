package tui

import (
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestBrowserRowsAndSelection(t *testing.T) {
	b := &browser{list: &proto.FSList{Path: "/home/dev/src", Parent: "/home/dev", Home: "/home/dev", Entries: []proto.FSEntry{
		{Name: "api", Git: true}, {Name: "Notes"}, {Name: "web", Project: true},
	}}}
	if rows := b.rows(); len(rows) != 4 || !rows[0].up {
		t.Fatalf("rows: %+v", rows)
	}
	b.sel = 1
	if p, _ := b.selectedPath(); p != "/home/dev/src/api" {
		t.Fatalf("selected %s", p)
	}
	b.sel = 0
	if p, _ := b.selectedPath(); p != "/home/dev" {
		t.Fatalf(".. selects %s", p)
	}
	// Filtering hides ".." and matches case-insensitively.
	b.filter = "NOT"
	if rows := b.rows(); len(rows) != 1 || rows[0].entry.Name != "Notes" {
		t.Fatalf("filtered: %+v", rows)
	}

	// A listing reply selects the folder we came up from.
	b2 := &browser{req: 1, want: "src"}
	m := &Model{}
	b2.update(m, dirListMsg{req: 1, list: proto.FSList{Path: "/home/dev", Parent: "/home", Entries: []proto.FSEntry{{Name: "docs"}, {Name: "src"}}}})
	if b2.sel != 2 {
		t.Fatalf("selection after going up: %d", b2.sel)
	}
	// Stale replies are ignored.
	b2.update(m, dirListMsg{req: 0, list: proto.FSList{Path: "/elsewhere"}})
	if b2.list.Path != "/home/dev" {
		t.Fatal("stale listing applied")
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"api": true, "my project": true, "": false, "..": false, "a/b": false} {
		if validName(name) != want {
			t.Errorf("validName(%q) = %v", name, !want)
		}
	}
}
